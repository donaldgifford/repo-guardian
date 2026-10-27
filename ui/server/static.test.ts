import { afterAll, beforeAll, describe, expect, test } from "bun:test";
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

import { createApp } from "./app";
import { loadConfig } from "./config";
import { discardLogger } from "./log";
import { contentSecurityPolicy } from "./static";
import { Browser } from "./testing/browser";
import { type MockIssuer, startMockIssuer } from "./testing/mock-issuer";
import { validEnv } from "./testenv";

let issuer: MockIssuer;
let base: string;
let dist: string;

beforeAll(async () => {
  issuer = await startMockIssuer();
  base = mkdtempSync(join(tmpdir(), "rg-ui-"));
  dist = join(base, "dist");
  mkdirSync(join(dist, "assets"), { recursive: true });
  writeFileSync(join(dist, "index.html"), "<!doctype html><div id=root></div>");
  writeFileSync(join(dist, "assets", "app-3f2a1b.js"), "console.log(1)");
  writeFileSync(join(dist, "favicon.svg"), "<svg/>");
  writeFileSync(join(base, "secret.txt"), "outside the root");
});

afterAll(() => {
  issuer.stop();
  rmSync(base, { recursive: true, force: true });
});

function setup() {
  const config = loadConfig({ ...validEnv, OIDC_ISSUER: issuer.url.toString() });
  const app = createApp({ config, log: discardLogger, staticDir: dist, upstreamFetch: async () => Response.json({}) });
  return new Browser(app);
}

async function signedIn() {
  const browser = setup();
  const login = await browser.request("/auth/login");
  const callback = await issuer.authorize(login.headers.get("location") ?? "");
  await browser.request(`${callback.pathname}${callback.search}`);
  return browser;
}

describe("static serving", () => {
  test("hashed assets are cached for a year", async () => {
    const res = await setup().request("/assets/app-3f2a1b.js");
    expect(res.status).toBe(200);
    expect(res.headers.get("cache-control")).toBe("public, max-age=31536000, immutable");
    expect(res.headers.get("content-type")).toContain("javascript");
  });

  test("a missing asset is 404, not the app shell", async () => {
    const res = await setup().request("/assets/gone-000000.js");
    expect(res.status).toBe(404);
  });

  test("index.html is never cached, and app paths fall back to it", async () => {
    const browser = await signedIn();
    for (const path of ["/", "/repos/acme/web", "/findings?status=non_compliant"]) {
      const res = await browser.request(path);
      expect(res.status).toBe(200);
      expect(res.headers.get("cache-control")).toBe("no-cache");
      expect(await res.text()).toContain('<div id=root>');
    }
  });

  test("app paths redirect to login without a session, keeping the path", async () => {
    const res = await setup().request("/repos/acme/web?tab=history");
    expect(res.status).toBe(302);
    expect(res.headers.get("location")).toBe(`/auth/login?return_to=${encodeURIComponent("/repos/acme/web?tab=history")}`);
  });

  test("/status is public", async () => {
    const res = await setup().request("/status");
    expect(res.status).toBe(200);
    expect(res.headers.get("cache-control")).toBe("no-cache");
  });

  test("root build files are served without a session", async () => {
    const res = await setup().request("/favicon.svg");
    expect(res.status).toBe(200);
    expect(res.headers.get("cache-control")).toBe("public, max-age=3600");
  });

  test("never serves a file outside the build", async () => {
    const browser = await signedIn();
    for (const path of ["/assets/..%2f..%2fsecret.txt", "/..%2fsecret.txt", "/assets/%2e%2e/%2e%2e/secret.txt"]) {
      const res = await browser.request(path);
      expect(await res.text()).not.toContain("outside the root");
    }
  });

  test("server prefixes never fall back to the app shell", async () => {
    const browser = await signedIn();
    for (const path of ["/auth/nope", "/ui/nope"]) {
      expect((await browser.request(path)).status).toBe(404);
    }
  });
});

describe("security headers", () => {
  test("every response carries CSP, nosniff and Referrer-Policy", async () => {
    const browser = setup();
    for (const path of ["/", "/status", "/assets/app-3f2a1b.js", "/healthz", "/api/v1/status"]) {
      const res = await browser.request(path);
      expect(res.headers.get("content-security-policy")).toBe(contentSecurityPolicy);
      expect(res.headers.get("x-content-type-options")).toBe("nosniff");
      expect(res.headers.get("referrer-policy")).toBe("same-origin");
    }
  });
});
