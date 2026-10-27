import { hkdfSync } from "node:crypto";

// Config is the BFF's validated environment (DESIGN-0027 § The UI).
export interface Config {
  port: number;
  oidc: {
    issuer: URL;
    clientId: string;
    clientSecret: string;
    scopes: string;
    // tokenAuthMethod is how the client secret reaches the token
    // endpoint (RFC 6749 §2.3.1); providers differ in which they accept.
    tokenAuthMethod: TokenAuthMethod;
    // authorizationParams are extra authorize-request parameters an
    // issuer needs (Auth0's audience, for one); never a reserved key.
    authorizationParams: Record<string, string>;
  };
  // apiUpstream is the api Service; the proxy never forwards anywhere else.
  apiUpstream: URL;
  // sessionKeys are the derived A256GCM keys: the first encrypts, any
  // decrypts, so a key can be rotated without logging everyone out.
  sessionKeys: Uint8Array[];
  sessionTtlSeconds: number;
  publicUrl: URL;
  // githubHost is where PR and repository links point (the API's
  // GITHUB_HOST); links are built from it, never from evidence URLs.
  githubHost: string;
  // docsUrl is the operator's documentation link in the sidebar; unset
  // hides the Docs group.
  docsUrl?: URL;
}

// ConfigError lists every invalid variable at once, so a misconfigured
// deployment fails on its first start with the whole list.
export class ConfigError extends Error {
  constructor(readonly problems: string[]) {
    super(`invalid configuration:\n  ${problems.join("\n  ")}`);
    this.name = "ConfigError";
  }
}

// TokenAuthMethod names the two client-authentication methods the BFF
// supports at the token endpoint.
export type TokenAuthMethod = "client_secret_post" | "client_secret_basic";

const tokenAuthMethods: readonly TokenAuthMethod[] = ["client_secret_post", "client_secret_basic"];

// reservedAuthorizationParams are set by the login flow itself; an
// operator may not override them through OIDC_AUTHORIZATION_PARAMS.
export const reservedAuthorizationParams: readonly string[] = [
  "redirect_uri",
  "scope",
  "state",
  "nonce",
  "code_challenge",
  "code_challenge_method",
  "response_type",
  "client_id",
];

const minKeyBytes = 32;
const defaultScopes = "openid profile email offline_access";
const defaultTtl = "8h";

// loadConfig validates env and fails fast on anything missing or malformed.
export function loadConfig(env: Record<string, string | undefined>): Config {
  const problems: string[] = [];

  const required = (name: string): string => {
    const v = env[name]?.trim();
    if (!v) {
      problems.push(`${name} is required`);
      return "";
    }
    return v;
  };

  // url returns undefined when the variable is missing or malformed, so
  // derived checks run only on a real value.
  const url = (name: string): URL | undefined => {
    const raw = required(name);
    if (!raw) {
      return undefined;
    }
    try {
      return new URL(raw);
    } catch {
      problems.push(`${name} must be an absolute URL, got ${JSON.stringify(raw)}`);
      return undefined;
    }
  };

  // optionalHTTPS reads a variable that may be absent; when set it must
  // be an https URL, since the browser follows it from the sidebar.
  const optionalHTTPS = (name: string): URL | undefined => {
    const raw = env[name]?.trim();
    if (!raw) {
      return undefined;
    }
    try {
      const u = new URL(raw);
      if (u.protocol !== "https:") {
        problems.push(`${name} must be an https URL, got ${JSON.stringify(raw)}`);
        return undefined;
      }
      return u;
    } catch {
      problems.push(`${name} must be an absolute https URL, got ${JSON.stringify(raw)}`);
      return undefined;
    }
  };

  const issuer = url("OIDC_ISSUER");
  if (issuer && issuer.protocol !== "https:" && !isLoopback(issuer)) {
    problems.push("OIDC_ISSUER must be https (http is allowed on localhost only)");
  }

  const clientId = required("OIDC_CLIENT_ID");
  const clientSecret = required("OIDC_CLIENT_SECRET");
  const apiUpstream = url("API_UPSTREAM");
  const publicUrl = url("PUBLIC_URL");

  if (publicUrl && (publicUrl.pathname !== "/" || publicUrl.search || publicUrl.hash)) {
    problems.push("PUBLIC_URL must be an origin with no path, query or fragment");
  }

  if (publicUrl && publicUrl.protocol !== "https:" && !isLoopback(publicUrl)) {
    problems.push("PUBLIC_URL must be https: the session cookie is Secure (http is allowed on localhost only)");
  }

  const scopes = env.OIDC_SCOPES?.trim() || defaultScopes;
  if (!scopes.split(/\s+/).includes("openid")) {
    problems.push("OIDC_SCOPES must include openid");
  }

  const tokenAuthMethod = parseTokenAuthMethod(env.OIDC_TOKEN_AUTH_METHOD, problems);
  const authorizationParams = parseAuthorizationParams(env.OIDC_AUTHORIZATION_PARAMS, problems);

  const keys = (env.UI_SESSION_KEYS ?? "")
    .split(",")
    .map((k) => k.trim())
    .filter(Boolean);
  if (keys.length === 0) {
    problems.push("UI_SESSION_KEYS is required: a comma-separated list, newest first");
  }
  keys.forEach((k, i) => {
    if (new TextEncoder().encode(k).length < minKeyBytes) {
      problems.push(`UI_SESSION_KEYS entry ${i + 1} is shorter than ${minKeyBytes} bytes`);
    }
  });

  const ttl = parseDuration(env.UI_SESSION_TTL?.trim() || defaultTtl);
  if (ttl === undefined || ttl <= 0) {
    problems.push(`UI_SESSION_TTL must be a positive duration like 8h or 90m, got ${JSON.stringify(env.UI_SESSION_TTL)}`);
  }

  const port = Number(env.PORT ?? 3000);
  if (!Number.isInteger(port) || port < 1 || port > 65535) {
    problems.push(`PORT must be a TCP port, got ${JSON.stringify(env.PORT)}`);
  }

  const githubHost = env.GITHUB_HOST ?? "github.com";
  if (!/^[A-Za-z0-9.-]+(:\d+)?$/.test(githubHost)) {
    problems.push(`GITHUB_HOST must be a host name like github.com, got ${JSON.stringify(env.GITHUB_HOST)}`);
  }

  const docsUrl = optionalHTTPS("UI_DOCS_URL");

  if (problems.length > 0 || !issuer || !apiUpstream || !publicUrl) {
    throw new ConfigError(problems);
  }

  return {
    port,
    oidc: { issuer, clientId, clientSecret, scopes, tokenAuthMethod, authorizationParams },
    apiUpstream,
    sessionKeys: keys.map(deriveKey),
    sessionTtlSeconds: ttl ?? 0,
    publicUrl,
    githubHost,
    ...(docsUrl ? { docsUrl } : {}),
  };
}

function parseTokenAuthMethod(raw: string | undefined, problems: string[]): TokenAuthMethod {
  const v = raw?.trim();
  if (!v) {
    return "client_secret_post";
  }
  const m = tokenAuthMethods.find((t) => t === v);
  if (!m) {
    problems.push(`OIDC_TOKEN_AUTH_METHOD must be one of ${tokenAuthMethods.join(", ")}, got ${JSON.stringify(raw)}`);
    return "client_secret_post";
  }
  return m;
}

// parseAuthorizationParams reads a JSON object of string values and
// reports every reserved or non-string key at once.
function parseAuthorizationParams(raw: string | undefined, problems: string[]): Record<string, string> {
  const v = raw?.trim();
  if (!v) {
    return {};
  }

  let parsed: unknown;
  try {
    parsed = JSON.parse(v);
  } catch {
    problems.push("OIDC_AUTHORIZATION_PARAMS must be a JSON object of string values");
    return {};
  }
  if (typeof parsed !== "object" || parsed === null || Array.isArray(parsed)) {
    problems.push("OIDC_AUTHORIZATION_PARAMS must be a JSON object of string values");
    return {};
  }

  const entries = Object.entries(parsed);
  const reserved = entries.map(([k]) => k).filter((k) => reservedAuthorizationParams.includes(k));
  if (reserved.length > 0) {
    problems.push(`OIDC_AUTHORIZATION_PARAMS may not set ${reserved.join(", ")}: the login flow sets them`);
  }
  const nonString = entries.filter(([, val]) => typeof val !== "string").map(([k]) => k);
  if (nonString.length > 0) {
    problems.push(`OIDC_AUTHORIZATION_PARAMS values must be strings: ${nonString.join(", ")}`);
  }

  const out: Record<string, string> = {};
  for (const [k, val] of entries) {
    if (typeof val === "string" && !reservedAuthorizationParams.includes(k)) {
      out[k] = val;
    }
  }
  return out;
}

// deriveKey stretches an operator secret into a 32-byte A256GCM key, so
// keys may be any string of at least 32 bytes rather than exact raw keys.
function deriveKey(secret: string): Uint8Array {
  return new Uint8Array(hkdfSync("sha256", secret, "", "repo-guardian-ui session v1", 32));
}

export function isLoopback(u: URL): boolean {
  return u.hostname === "localhost" || u.hostname === "127.0.0.1" || u.hostname === "[::1]";
}

const unitSeconds: Record<string, number> = { s: 1, m: 60, h: 3600 };

// parseDuration reads a Go-style duration of s/m/h parts ("8h", "1h30m")
// into seconds; undefined when it does not parse.
export function parseDuration(s: string): number | undefined {
  const re = /(\d+)([smh])/g;
  let total = 0;
  let consumed = 0;

  for (const m of s.matchAll(re)) {
    if (m.index !== consumed) {
      return undefined;
    }
    total += Number(m[1]) * (unitSeconds[m[2] ?? ""] ?? 0);
    consumed += m[0].length;
  }

  return consumed === s.length && consumed > 0 ? total : undefined;
}

// PublicConfig is what /ui/config tells the browser: display values only,
// never a secret, key or upstream address.
export interface PublicConfig {
  issuer: string;
  public_url: string;
  session_ttl_seconds: number;
  github_host: string;
  docs_url?: string;
}

export function publicConfig(cfg: Config): PublicConfig {
  return {
    issuer: cfg.oidc.issuer.origin,
    public_url: cfg.publicUrl.origin,
    session_ttl_seconds: cfg.sessionTtlSeconds,
    github_host: cfg.githubHost,
    ...(cfg.docsUrl ? { docs_url: cfg.docsUrl.href } : {}),
  };
}
