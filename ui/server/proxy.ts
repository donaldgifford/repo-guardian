import type { Context, Hono } from "hono";
import type { StatusCode } from "hono/utils/http-status";
import * as client from "openid-client";

import type { Config } from "./config";
import type { Logger } from "./log";
import type { Oidc } from "./oidc";
import type { SealedCookie, Session } from "./session";

// publicApiPaths are the operations api/openapi.yaml declares with
// `security: []`; they are proxied without a token. proxy.test.ts checks
// this list against the spec.
export const publicApiPaths = new Set(["/api/v1/status", "/api/v1/openapi.yaml"]);

// refreshSkewSeconds: a token expiring within this window is refreshed
// before it is forwarded.
const refreshSkewSeconds = 60;

// hopByHop headers never cross the proxy (RFC 9110 § 7.6.1), nor do
// Cookie (the session is ours, not the API's) and Host.
const dropRequest = new Set([
  "connection",
  "keep-alive",
  "proxy-authenticate",
  "proxy-authorization",
  "proxy-connection",
  "te",
  "trailer",
  "transfer-encoding",
  "upgrade",
  "host",
  "cookie",
  "authorization",
  "content-length",
]);

const dropResponse = new Set([
  "connection",
  "keep-alive",
  "proxy-authenticate",
  "proxy-connection",
  "te",
  "trailer",
  "transfer-encoding",
  "upgrade",
  "set-cookie",
  "content-encoding",
  "content-length",
]);

// UpstreamFetch is the slice of fetch the proxy uses.
export type UpstreamFetch = (url: URL, init: RequestInit) => Promise<Response>;

export interface ProxyDeps {
  config: Config;
  oidc: Oidc;
  session: SealedCookie<Session>;
  log: Logger;
  // fetch is injectable for tests; it defaults to the global fetch.
  fetch?: UpstreamFetch;
}

// mountProxy forwards /api/* to API_UPSTREAM (DESIGN-0027 § The UI). It
// reads only: GET and HEAD. The Authorization header comes from exactly
// one place: the caller's own Bearer when it sent one, else the session.
export function mountProxy(app: Hono, deps: ProxyDeps): void {
  const upstreamFetch: UpstreamFetch = deps.fetch ?? fetch;

  app.all("/api/*", async (c) => {
    const method = c.req.method;
    if (method !== "GET" && method !== "HEAD") {
      return c.json({ error: "method_not_allowed", message: "the API is read-only" }, 405, { allow: "GET, HEAD" });
    }

    const requestId = c.req.header("x-request-id") || crypto.randomUUID();
    const reqUrl = new URL(c.req.url);

    const auth = await authorization(c, deps, publicApiPaths.has(reqUrl.pathname));
    if (auth.kind === "unauthenticated") {
      return c.json({ error: "unauthenticated", message: "sign in at /auth/login" }, 401, { "x-request-id": requestId });
    }

    // The target is always the configured upstream's origin plus the
    // request's path: nothing in the request can choose the host.
    const target = new URL(`${deps.config.apiUpstream.origin}${reqUrl.pathname}${reqUrl.search}`);

    const headers = new Headers();
    const connectionListed = new Set(
      (c.req.header("connection") ?? "")
        .split(",")
        .map((h) => h.trim().toLowerCase())
        .filter(Boolean),
    );
    c.req.raw.headers.forEach((v, k) => {
      if (!dropRequest.has(k) && !connectionListed.has(k)) {
        headers.set(k, v);
      }
    });
    headers.set("x-request-id", requestId);
    if (auth.kind === "bearer") {
      headers.set("authorization", auth.header);
    }

    let upstream: Response;
    try {
      upstream = await upstreamFetch(target, { method, headers, redirect: "manual" });
    } catch (err) {
      deps.log("error", "api upstream unreachable", { request_id: requestId, error: String(err) });
      return c.json({ error: "bad_gateway", message: "the API is unreachable" }, 502, { "x-request-id": requestId });
    }

    // Built through c so a session cookie rewritten by a refresh is kept:
    // a bare Response would drop the headers queued on the context.
    upstream.headers.forEach((v, k) => {
      if (!dropResponse.has(k)) {
        c.header(k, v, { append: true });
      }
    });
    c.header("x-request-id", requestId);
    c.status(upstream.status as StatusCode);

    return c.newResponse(method === "HEAD" ? null : upstream.body);
  });
}

type Authorization = { kind: "bearer"; header: string } | { kind: "anonymous" } | { kind: "unauthenticated" };

async function authorization(c: Context, deps: ProxyDeps, isPublic: boolean): Promise<Authorization> {
  const own = c.req.header("authorization");
  if (own && /^Bearer\s+\S/i.test(own)) {
    return { kind: "bearer", header: own };
  }

  const s = await deps.session.read(c);
  if (!s) {
    return isPublic ? { kind: "anonymous" } : { kind: "unauthenticated" };
  }

  const fresh = await ensureFresh(c, deps, s);
  if (!fresh) {
    deps.session.clear(c);
    return isPublic ? { kind: "anonymous" } : { kind: "unauthenticated" };
  }

  return { kind: "bearer", header: `Bearer ${fresh.accessToken}` };
}

// ensureFresh refreshes the access token when it expires within the skew
// and writes the refreshed session back. It returns undefined when the
// token is stale and cannot be refreshed.
async function ensureFresh(c: Context, deps: ProxyDeps, s: Session): Promise<Session | undefined> {
  const now = Math.floor(Date.now() / 1000);
  if (s.accessTokenExpiresAt - now > refreshSkewSeconds) {
    return s;
  }

  if (!s.refreshToken) {
    return s.accessTokenExpiresAt > now ? s : undefined;
  }

  try {
    const tokens = await client.refreshTokenGrant(await deps.oidc.configuration(), s.refreshToken);
    const next: Session = {
      ...s,
      accessToken: tokens.access_token,
      accessTokenExpiresAt: now + (tokens.expiresIn() ?? 0),
      refreshToken: tokens.refresh_token ?? s.refreshToken,
      ...(tokens.id_token ? { idToken: tokens.id_token } : {}),
    };
    // expiresAt is carried over: a refresh never extends the session.
    await deps.session.write(c, next, next.expiresAt);
    return next;
  } catch (err) {
    deps.log("warn", "token refresh failed; signing out", { sub: s.sub, error: String(err) });
    return undefined;
  }
}
