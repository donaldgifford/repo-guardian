import { hkdfSync } from "node:crypto";

// Config is the BFF's validated environment (DESIGN-0027 § The UI).
export interface Config {
  port: number;
  oidc: {
    issuer: URL;
    clientId: string;
    clientSecret: string;
    scopes: string;
  };
  // apiUpstream is the api Service; the proxy never forwards anywhere else.
  apiUpstream: URL;
  // sessionKeys are the derived A256GCM keys: the first encrypts, any
  // decrypts, so a key can be rotated without logging everyone out.
  sessionKeys: Uint8Array[];
  sessionTtlSeconds: number;
  publicUrl: URL;
}

// ConfigError lists every invalid variable at once, so a misconfigured
// deployment fails on its first start with the whole list.
export class ConfigError extends Error {
  constructor(readonly problems: string[]) {
    super(`invalid configuration:\n  ${problems.join("\n  ")}`);
    this.name = "ConfigError";
  }
}

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

  const issuer = url("OIDC_ISSUER");
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

  if (problems.length > 0 || !issuer || !apiUpstream || !publicUrl) {
    throw new ConfigError(problems);
  }

  return {
    port,
    oidc: { issuer, clientId, clientSecret, scopes },
    apiUpstream,
    sessionKeys: keys.map(deriveKey),
    sessionTtlSeconds: ttl ?? 0,
    publicUrl,
  };
}

// deriveKey stretches an operator secret into a 32-byte A256GCM key, so
// keys may be any string of at least 32 bytes rather than exact raw keys.
function deriveKey(secret: string): Uint8Array {
  return new Uint8Array(hkdfSync("sha256", secret, "", "repo-guardian-ui session v1", 32));
}

function isLoopback(u: URL): boolean {
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
}

export function publicConfig(cfg: Config): PublicConfig {
  return {
    issuer: cfg.oidc.issuer.origin,
    public_url: cfg.publicUrl.origin,
    session_ttl_seconds: cfg.sessionTtlSeconds,
  };
}
