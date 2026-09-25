import { afterAll, beforeAll, describe, expect, test } from "bun:test";

import { createApp } from "./app";
import { loadConfig } from "./config";
import { discardLogger } from "./log";
import { publicApiPaths } from "./proxy";
import { sessionCookie } from "./session";
import { Browser } from "./testing/browser";
import { type MockIssuer, startMockIssuer } from "./testing/mock-issuer";
import { validEnv } from "./testenv";

interface Seen {
  url: string;
  method: string;
  headers: Headers;
}

let issuer: MockIssuer;
let shortIssuer: MockIssuer;

beforeAll(async () => {
  issuer = await startMockIssuer();
  // Tokens that are already inside the refresh window when issued.
  shortIssuer = await startMockIssuer({ accessTokenTtl: 30 });
});

afterAll(() => {
  issuer.stop();
  shortIssuer.stop();
});

function setup(from: MockIssuer = issuer, reply: () => Response = () => Response.json({ ok: true })) {
  const seen: Seen[] = [];
  const upstreamFetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
    seen.push({ url: input.toString(), method: init?.method ?? "GET", headers: new Headers(init?.headers) });
    return reply();
  }) as typeof fetch;

  const config = loadConfig({ ...validEnv, OIDC_ISSUER: from.url.toString() });
  const app = createApp({ config, log: discardLogger, upstreamFetch });
  return { config, app, seen, browser: new Browser(app) };
}

async function signIn(from: MockIssuer = issuer, reply?: () => Response) {
  const s = setup(from, reply);
  const login = await s.browser.request("/auth/login");
  const callback = await from.authorize(login.headers.get("location") ?? "");
  await s.browser.request(`${callback.pathname}${callback.search}`);
  return s;
}

describe("api proxy", () => {
  test("forwards a signed-in GET to the upstream with the session's token", async () => {
    const { browser, seen } = await signIn();
    const res = await browser.request("/api/v1/findings?status=non_compliant");
    expect(res.status).toBe(200);
    expect(seen).toHaveLength(1);
    expect(seen[0]?.url).toBe("http://repo-guardian-api/api/v1/findings?status=non_compliant");
    expect(seen[0]?.headers.get("authorization")).toMatch(/^Bearer \S+$/);
    expect(seen[0]?.headers.get("cookie")).toBeNull();
  });

  test("rejects anything but GET and HEAD", async () => {
    const { browser, seen } = await signIn();
    for (const method of ["POST", "PUT", "PATCH", "DELETE"]) {
      const res = await browser.request("/api/v1/findings", { method });
      expect(res.status).toBe(405);
      expect(res.headers.get("allow")).toBe("GET, HEAD");
    }
    expect(seen).toHaveLength(0);
  });

  test("HEAD is forwarded without a body", async () => {
    const { browser, seen } = await signIn();
    const res = await browser.request("/api/v1/summary", { method: "HEAD" });
    expect(res.status).toBe(200);
    expect(await res.text()).toBe("");
    expect(seen[0]?.method).toBe("HEAD");
  });

  test("the target host never comes from the request", async () => {
    const { browser, seen } = await signIn();
    await browser.request("/api/v1/summary", {
      headers: { host: "evil.example", "x-forwarded-host": "evil.example", forwarded: "host=evil.example" },
    });
    expect(new URL(seen[0]?.url ?? "").host).toBe("repo-guardian-api");
  });

  test("strips hop-by-hop headers and any header named in Connection", async () => {
    const { browser, seen } = await signIn();
    await browser.request("/api/v1/summary", {
      headers: {
        connection: "keep-alive, x-secret-hop",
        "keep-alive": "timeout=5",
        "x-secret-hop": "1",
        "proxy-authorization": "Basic Zm9vOmJhcg==",
        te: "trailers",
        upgrade: "websocket",
        accept: "application/json",
      },
    });
    const h = seen[0]?.headers ?? new Headers();
    for (const name of ["connection", "keep-alive", "x-secret-hop", "proxy-authorization", "te", "upgrade", "cookie"]) {
      expect(h.get(name)).toBeNull();
    }
    expect(h.get("accept")).toBe("application/json");
  });

  test("strips hop-by-hop and Set-Cookie from the upstream response", async () => {
    const { browser } = await signIn(issuer, () =>
      Response.json({}, { headers: { "set-cookie": "upstream=1", connection: "close", "cache-control": "no-store" } }),
    );
    const res = await browser.request("/api/v1/summary");
    expect(res.headers.getSetCookie()).toHaveLength(0);
    expect(res.headers.get("connection")).toBeNull();
    expect(res.headers.get("cache-control")).toBe("no-store");
  });

  test("a caller's own Bearer passes through untouched, without the session", async () => {
    const { browser, seen } = await signIn();
    await browser.request("/api/v1/summary", { headers: { authorization: "Bearer caller-token" } });
    expect(seen[0]?.headers.get("authorization")).toBe("Bearer caller-token");
  });

  test("a caller's Bearer works with no session at all", async () => {
    const { browser, seen } = setup();
    const res = await browser.request("/api/v1/summary", { headers: { authorization: "Bearer caller-token" } });
    expect(res.status).toBe(200);
    expect(seen[0]?.headers.get("authorization")).toBe("Bearer caller-token");
  });

  test("an anonymous call to a private endpoint is 401 and never reaches the upstream", async () => {
    const { browser, seen } = setup();
    const res = await browser.request("/api/v1/findings");
    expect(res.status).toBe(401);
    expect(seen).toHaveLength(0);
  });

  test("public endpoints go without a token", async () => {
    const { browser, seen } = setup();
    for (const path of publicApiPaths) {
      const res = await browser.request(path);
      expect(res.status).toBe(200);
    }
    expect(seen).toHaveLength(publicApiPaths.size);
    for (const s of seen) {
      expect(s.headers.get("authorization")).toBeNull();
    }
  });

  test("propagates X-Request-ID, minting one when absent", async () => {
    const { browser, seen } = await signIn();
    const given = await browser.request("/api/v1/summary", { headers: { "x-request-id": "req-123" } });
    expect(given.headers.get("x-request-id")).toBe("req-123");
    expect(seen[0]?.headers.get("x-request-id")).toBe("req-123");

    const minted = await browser.request("/api/v1/summary");
    const id = minted.headers.get("x-request-id");
    expect(id).toMatch(/^[0-9a-f-]{36}$/);
    expect(seen[1]?.headers.get("x-request-id")).toBe(id);
  });

  test("an unreachable upstream is 502", async () => {
    const { browser } = await signIn(issuer, () => {
      throw new TypeError("connection refused");
    });
    const res = await browser.request("/api/v1/summary");
    expect(res.status).toBe(502);
  });
});

describe("token refresh", () => {
  test("refreshes a token within 60s of expiry and keeps the session's end", async () => {
    const { browser, seen, config } = await signIn(shortIssuer);
    const cookies = sessionCookie(config.sessionKeys);
    const before = await cookies.open(browser.cookie("__Host-rg_session") ?? "");
    const refreshes = shortIssuer.refreshes;

    await browser.request("/api/v1/summary");

    expect(shortIssuer.refreshes).toBe(refreshes + 1);
    const after = await cookies.open(browser.cookie("__Host-rg_session") ?? "");
    expect(after?.accessToken).not.toBe(before?.accessToken);
    expect(after?.expiresAt).toBe(before?.expiresAt ?? -1);
    expect(seen[0]?.headers.get("authorization")).toBe(`Bearer ${after?.accessToken}`);
  });

  test("does not refresh a token with time to spare", async () => {
    const { browser } = await signIn();
    const refreshes = issuer.refreshes;
    await browser.request("/api/v1/summary");
    expect(issuer.refreshes).toBe(refreshes);
  });

  test("a failed refresh signs the user out", async () => {
    const { browser, seen, config } = await signIn(shortIssuer);
    const cookies = sessionCookie(config.sessionKeys);
    const s = await cookies.open(browser.cookie("__Host-rg_session") ?? "");
    if (!s) {
      throw new Error("no session");
    }
    // Replace the session with one whose refresh token the issuer never issued.
    browser.jar.set("__Host-rg_session", await cookies.seal({ ...s, refreshToken: "forged" }, s.expiresAt));

    const res = await browser.request("/api/v1/findings");
    expect(res.status).toBe(401);
    expect(seen).toHaveLength(0);
    expect(browser.cookie("__Host-rg_session")).toBeUndefined();
  });
});

test("publicApiPaths matches the operations the spec leaves unauthenticated", async () => {
  const spec = await Bun.file(new URL("../../api/openapi.yaml", import.meta.url)).text();
  const doc = Bun.YAML.parse(spec) as {
    servers: { url: string }[];
    paths: Record<string, Record<string, { security?: unknown[] }>>;
  };
  const base = doc.servers[0]?.url ?? "";
  const open = Object.entries(doc.paths)
    .filter(([, ops]) => Object.values(ops).some((op) => Array.isArray(op.security) && op.security.length === 0))
    .map(([p]) => `${base}${p}`);
  expect(new Set(open)).toEqual(publicApiPaths);
});
