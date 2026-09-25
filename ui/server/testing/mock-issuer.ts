// A minimal OIDC provider for the BFF's tests (IMPL-0025 OQ25): discovery,
// JWKS, an authorization endpoint that approves at once, the token
// endpoint (authorization_code with PKCE, refresh_token), RFC 7009
// revocation and an end-session endpoint. It records what it saw so tests
// can assert on it.
import { createHash, randomBytes } from "node:crypto";
import { exportJWK, generateKeyPair, SignJWT } from "jose";

interface PendingCode {
  clientId: string;
  redirectUri: string;
  nonce: string;
  challenge: string;
  sub: string;
}

export interface MockIssuerOptions {
  clientId?: string;
  clientSecret?: string;
  // accessTokenTtl is the access token lifetime in seconds.
  accessTokenTtl?: number;
  sub?: string;
  name?: string;
  // apiAudience, when set, makes access tokens RS256 JWTs for that
  // audience (what the Go API verifies) instead of opaque strings.
  apiAudience?: string;
  // groups is the access token's groups claim.
  groups?: string[];
}

export interface MockIssuer {
  url: URL;
  clientId: string;
  clientSecret: string;
  revoked: string[];
  refreshes: number;
  // authorize follows the authorization URL the way a browser would and
  // returns the callback URL the issuer redirects back to.
  authorize(authorizationUrl: string): Promise<URL>;
  // issueTokenFor mints a token set without a browser round trip.
  stop(): void;
}

export async function startMockIssuer(opts: MockIssuerOptions = {}): Promise<MockIssuer> {
  const clientId = opts.clientId ?? "repo-guardian-ui";
  const clientSecret = opts.clientSecret ?? "client-secret";
  const accessTokenTtl = opts.accessTokenTtl ?? 300;
  const sub = opts.sub ?? "user-1";
  const name = opts.name ?? "Ada Lovelace";

  const { privateKey, publicKey } = await generateKeyPair("RS256");
  const jwk = { ...(await exportJWK(publicKey)), kid: "k1", alg: "RS256", use: "sig" };

  const codes = new Map<string, PendingCode>();
  const refreshTokens = new Set<string>();
  const state: Pick<MockIssuer, "revoked" | "refreshes"> = { revoked: [], refreshes: 0 };

  let base = "";

  const idToken = (nonce: string | undefined) =>
    new SignJWT({ nonce, name, preferred_username: "ada" })
      .setProtectedHeader({ alg: "RS256", kid: "k1" })
      .setIssuer(base)
      .setAudience(clientId)
      .setSubject(sub)
      .setIssuedAt()
      .setExpirationTime("5m")
      .sign(privateKey);

  // accessToken is opaque unless an API audience is configured; then it
  // is a JWT access token (typ at+jwt, no nonce) the API accepts.
  const accessToken = async () => {
    if (!opts.apiAudience) {
      return `at-${randomBytes(8).toString("hex")}`;
    }
    return new SignJWT({ azp: clientId, groups: opts.groups ?? [], name, preferred_username: "ada" })
      .setProtectedHeader({ alg: "RS256", kid: "k1", typ: "at+jwt" })
      .setIssuer(base)
      .setAudience(opts.apiAudience)
      .setSubject(sub)
      .setIssuedAt()
      .setExpirationTime(`${accessTokenTtl}s`)
      .sign(privateKey);
  };

  const tokens = async (nonce: string | undefined) => {
    const refresh = randomBytes(16).toString("hex");
    refreshTokens.add(refresh);
    return {
      access_token: await accessToken(),
      token_type: "Bearer",
      expires_in: accessTokenTtl,
      refresh_token: refresh,
      id_token: await idToken(nonce),
    };
  };

  const clientOK = (form: URLSearchParams) => form.get("client_id") === clientId && form.get("client_secret") === clientSecret;

  const server = Bun.serve({
    port: 0,
    async fetch(req) {
      const url = new URL(req.url);
      switch (url.pathname) {
        case "/.well-known/openid-configuration":
          return Response.json({
            issuer: base,
            authorization_endpoint: `${base}/authorize`,
            token_endpoint: `${base}/token`,
            jwks_uri: `${base}/jwks`,
            revocation_endpoint: `${base}/revoke`,
            end_session_endpoint: `${base}/logout`,
            response_types_supported: ["code"],
            subject_types_supported: ["public"],
            id_token_signing_alg_values_supported: ["RS256"],
            code_challenge_methods_supported: ["S256"],
          });
        case "/jwks":
          return Response.json({ keys: [jwk] });
        case "/authorize": {
          const p = url.searchParams;
          const code = randomBytes(12).toString("hex");
          codes.set(code, {
            clientId: p.get("client_id") ?? "",
            redirectUri: p.get("redirect_uri") ?? "",
            nonce: p.get("nonce") ?? "",
            challenge: p.get("code_challenge") ?? "",
            sub,
          });
          const back = new URL(p.get("redirect_uri") ?? "");
          back.searchParams.set("code", code);
          back.searchParams.set("state", p.get("state") ?? "");
          back.searchParams.set("iss", base);
          return Response.redirect(back.toString(), 302);
        }
        case "/token": {
          const form = new URLSearchParams(await req.text());
          if (!clientOK(form)) {
            return Response.json({ error: "invalid_client" }, { status: 401 });
          }
          if (form.get("grant_type") === "authorization_code") {
            const pending = codes.get(form.get("code") ?? "");
            codes.delete(form.get("code") ?? "");
            const verifier = form.get("code_verifier") ?? "";
            const challenge = createHash("sha256").update(verifier).digest("base64url");
            if (!pending || pending.challenge !== challenge || pending.redirectUri !== form.get("redirect_uri")) {
              return Response.json({ error: "invalid_grant" }, { status: 400 });
            }
            return Response.json(await tokens(pending.nonce));
          }
          if (form.get("grant_type") === "refresh_token") {
            const rt = form.get("refresh_token") ?? "";
            if (!refreshTokens.delete(rt)) {
              return Response.json({ error: "invalid_grant" }, { status: 400 });
            }
            state.refreshes++;
            return Response.json(await tokens(undefined));
          }
          return Response.json({ error: "unsupported_grant_type" }, { status: 400 });
        }
        case "/revoke": {
          const form = new URLSearchParams(await req.text());
          if (!clientOK(form)) {
            return Response.json({ error: "invalid_client" }, { status: 401 });
          }
          state.revoked.push(form.get("token") ?? "");
          refreshTokens.delete(form.get("token") ?? "");
          return new Response(null, { status: 200 });
        }
        case "/logout":
          return new Response("logged out");
        default:
          return new Response("not found", { status: 404 });
      }
    },
  });

  base = `http://127.0.0.1:${server.port}`;

  const issuer: MockIssuer = {
    url: new URL(base),
    clientId,
    clientSecret,
    get revoked() {
      return state.revoked;
    },
    get refreshes() {
      return state.refreshes;
    },
    async authorize(authorizationUrl) {
      const res = await fetch(authorizationUrl, { redirect: "manual" });
      return new URL(res.headers.get("location") ?? "");
    },
    stop: () => server.stop(true),
  };

  return issuer;
}
