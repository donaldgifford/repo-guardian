import type { Hono } from "hono";
import * as client from "openid-client";

import type { Config } from "./config";
import type { Logger } from "./log";
import type { Oidc } from "./oidc";
import { safeReturnPath } from "./redirect";
import { newSessionExpiry, SealedCookie, type Session } from "./session";

// LoginTransaction is what /auth/login leaves for /auth/callback, in a
// short-lived encrypted cookie.
interface LoginTransaction {
  verifier: string;
  state: string;
  nonce: string;
  returnTo: string;
}

// loginTtlSeconds bounds how long a login may sit at the issuer.
const loginTtlSeconds = 600;

export interface AuthDeps {
  config: Config;
  oidc: Oidc;
  session: SealedCookie<Session>;
  log: Logger;
}

// mountAuth adds /auth/login, /auth/callback and /auth/logout
// (DESIGN-0027 § The UI).
export function mountAuth(app: Hono, { config, oidc, session, log }: AuthDeps): void {
  const login = new SealedCookie<LoginTransaction>("rg_login", config.sessionKeys);
  const redirectUri = new URL("/auth/callback", config.publicUrl).toString();

  app.get("/auth/login", async (c) => {
    const oc = await oidc.configuration();
    const tx: LoginTransaction = {
      verifier: client.randomPKCECodeVerifier(),
      state: client.randomState(),
      nonce: client.randomNonce(),
      returnTo: safeReturnPath(c.req.query("return_to"), config.publicUrl),
    };

    await login.write(c, tx, newSessionExpiry(loginTtlSeconds));

    const url = client.buildAuthorizationUrl(oc, {
      redirect_uri: redirectUri,
      scope: config.oidc.scopes,
      code_challenge: await client.calculatePKCECodeChallenge(tx.verifier),
      code_challenge_method: "S256",
      state: tx.state,
      nonce: tx.nonce,
    });

    return c.redirect(url.toString(), 302);
  });

  app.get("/auth/callback", async (c) => {
    const tx = await login.read(c);
    login.clear(c);

    if (!tx) {
      return c.text("login expired or was not started here; start again at /auth/login", 400);
    }

    // The issuer redirected the browser to PUBLIC_URL; behind a proxy the
    // request URL may name an internal host, so rebuild it.
    const reqUrl = new URL(c.req.url);
    const current = new URL(`${reqUrl.pathname}${reqUrl.search}`, config.publicUrl);

    let tokens: Awaited<ReturnType<typeof client.authorizationCodeGrant>>;
    try {
      tokens = await client.authorizationCodeGrant(await oidc.configuration(), current, {
        pkceCodeVerifier: tx.verifier,
        expectedState: tx.state,
        expectedNonce: tx.nonce,
        idTokenExpected: true,
      });
    } catch (err) {
      log("warn", "login callback rejected", { error: String(err) });
      return c.text("login failed", 400);
    }

    const claims = tokens.claims();
    if (!claims) {
      return c.text("login failed", 400);
    }

    const now = Math.floor(Date.now() / 1000);
    const s: Session = {
      sub: claims.sub,
      name: displayName(claims),
      accessToken: tokens.access_token,
      accessTokenExpiresAt: now + (tokens.expiresIn() ?? 0),
      expiresAt: newSessionExpiry(config.sessionTtlSeconds),
      ...(tokens.refresh_token ? { refreshToken: tokens.refresh_token } : {}),
      ...(tokens.id_token ? { idToken: tokens.id_token } : {}),
    };

    await session.write(c, s, s.expiresAt);
    log("info", "signed in", { sub: s.sub });

    return c.redirect(tx.returnTo, 302);
  });

  app.post("/auth/logout", async (c) => {
    // CSRF: a logout must come from our own pages.
    if (c.req.header("origin") !== config.publicUrl.origin) {
      return c.text("forbidden", 403);
    }

    const s = await session.read(c);
    session.clear(c);

    let target = "/";
    try {
      const oc = await oidc.configuration();
      const meta = oc.serverMetadata();

      if (s?.refreshToken && meta.revocation_endpoint) {
        await client.tokenRevocation(oc, s.refreshToken, { token_type_hint: "refresh_token" });
      }

      if (meta.end_session_endpoint) {
        target = client
          .buildEndSessionUrl(oc, {
            post_logout_redirect_uri: new URL("/", config.publicUrl).toString(),
            ...(s?.idToken ? { id_token_hint: s.idToken } : {}),
          })
          .toString();
      }
    } catch (err) {
      // The local session is gone either way; the issuer side is best effort.
      log("warn", "logout: issuer revocation or end-session failed", { error: String(err) });
    }

    return c.redirect(target, 303);
  });
}

function displayName(claims: { sub: string; [k: string]: unknown }): string {
  for (const k of ["name", "preferred_username", "email"]) {
    const v = claims[k];
    if (typeof v === "string" && v) {
      return v;
    }
  }
  return claims.sub;
}
