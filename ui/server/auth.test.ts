import { afterAll, beforeAll, describe, expect, test } from "bun:test";

import { createApp } from "./app";
import { loadConfig } from "./config";
import { discardLogger } from "./log";
import { sessionCookie } from "./session";
import { Browser } from "./testing/browser";
import { type MockIssuer, startMockIssuer } from "./testing/mock-issuer";
import { validEnv } from "./testenv";

let issuer: MockIssuer;

beforeAll(async () => {
  issuer = await startMockIssuer();
});

afterAll(() => issuer.stop());

function setup() {
  const config = loadConfig({ ...validEnv, OIDC_ISSUER: issuer.url.toString() });
  const app = createApp({ config, log: discardLogger });
  return { config, app, browser: new Browser(app) };
}

// signIn runs the full code flow and returns the browser holding a session.
async function signIn(returnTo = "/findings?status=non_compliant") {
  const s = setup();
  const login = await s.browser.request(`/auth/login?return_to=${encodeURIComponent(returnTo)}`);
  const callback = await issuer.authorize(login.headers.get("location") ?? "");
  const res = await s.browser.request(`${callback.pathname}${callback.search}`);
  return { ...s, login, res };
}

describe("login", () => {
  test("redirects to the issuer with PKCE, state and nonce", async () => {
    const { browser } = setup();
    const res = await browser.request("/auth/login");
    expect(res.status).toBe(302);

    const to = new URL(res.headers.get("location") ?? "");
    expect(to.origin).toBe(issuer.url.origin);
    for (const p of ["state", "nonce", "code_challenge"]) {
      expect(to.searchParams.get(p)).toBeTruthy();
    }
    expect(to.searchParams.get("code_challenge_method")).toBe("S256");
    expect(to.searchParams.get("redirect_uri")).toBe("https://guardian.example.com/auth/callback");
    expect(browser.cookie("__Host-rg_login")).toBeTruthy();
  });

  test("the callback sets the session and returns to the original path", async () => {
    const { res, browser, config } = await signIn();
    expect(res.status).toBe(302);
    expect(res.headers.get("location")).toBe("/findings?status=non_compliant");
    expect(browser.cookie("__Host-rg_login")).toBeUndefined();

    const set = res.headers.getSetCookie().find((c) => c.startsWith("__Host-rg_session=")) ?? "";
    for (const attr of ["HttpOnly", "Secure", "SameSite=Lax", "Path=/"]) {
      expect(set).toContain(attr);
    }
    expect(set).not.toContain("Domain");

    const token = browser.cookie("__Host-rg_session") ?? "";
    const s = await sessionCookie(config.sessionKeys).open(token);
    expect(s).toMatchObject({ sub: "user-1", name: "Ada Lovelace" });
    expect(s?.refreshToken).toBeTruthy();
    expect((s?.expiresAt ?? 0) - Math.floor(Date.now() / 1000)).toBeGreaterThan(8 * 3600 - 5);
  });

  test("never redirects off-origin after login", async () => {
    for (const evil of ["https://evil.example", "//evil.example", "/\\evil.example", "javascript:alert(1)"]) {
      const { res } = await signIn(evil);
      expect(res.headers.get("location")).toBe("/");
    }
  });

  test("rejects a callback whose state does not match", async () => {
    const { browser } = setup();
    const login = await browser.request("/auth/login");
    const callback = await issuer.authorize(login.headers.get("location") ?? "");
    callback.searchParams.set("state", "forged");

    const res = await browser.request(`${callback.pathname}${callback.search}`);
    expect(res.status).toBe(400);
    expect(browser.cookie("__Host-rg_session")).toBeUndefined();
  });

  test("rejects a callback with no login in progress", async () => {
    const { browser } = setup();
    const res = await browser.request("/auth/callback?code=x&state=y");
    expect(res.status).toBe(400);
  });
});

describe("logout", () => {
  test("checks Origin", async () => {
    const { browser } = await signIn();
    const res = await browser.request("/auth/logout", { method: "POST", headers: { origin: "https://evil.example" } });
    expect(res.status).toBe(403);
    expect(browser.cookie("__Host-rg_session")).toBeTruthy();
  });

  test("revokes the refresh token, clears the session and ends the issuer session", async () => {
    const { browser, config } = await signIn();
    const s = await sessionCookie(config.sessionKeys).open(browser.cookie("__Host-rg_session") ?? "");

    const res = await browser.request("/auth/logout", { method: "POST", headers: { origin: "https://guardian.example.com" } });
    expect(res.status).toBe(303);
    expect(browser.cookie("__Host-rg_session")).toBeUndefined();
    expect(issuer.revoked).toContain(s?.refreshToken ?? "missing");

    const to = new URL(res.headers.get("location") ?? "");
    expect(`${to.origin}${to.pathname}`).toBe(`${issuer.url.origin}/logout`);
    expect(to.searchParams.get("post_logout_redirect_uri")).toBe("https://guardian.example.com/");
    expect(to.searchParams.get("id_token_hint")).toBeTruthy();
  });
});
