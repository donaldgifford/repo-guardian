import { describe, expect, test } from "bun:test";

import { ConfigError, loadConfig, parseDuration, publicConfig } from "./config";
import { testKey as key, validEnv } from "./testenv";

function problems(env: Record<string, string | undefined>): string[] {
  try {
    loadConfig(env);
  } catch (e) {
    if (e instanceof ConfigError) {
      return e.problems;
    }
    throw e;
  }
  return [];
}

describe("loadConfig", () => {
  test("accepts a complete environment with defaults", () => {
    const cfg = loadConfig(validEnv);
    expect(cfg.oidc.scopes).toBe("openid profile email offline_access");
    expect(cfg.sessionTtlSeconds).toBe(8 * 3600);
    expect(cfg.port).toBe(3000);
    expect(cfg.sessionKeys).toHaveLength(1);
    expect(cfg.sessionKeys[0]).toHaveLength(32);
  });

  test("reports every missing variable at once", () => {
    const got = problems({});
    for (const name of ["OIDC_ISSUER", "OIDC_CLIENT_ID", "OIDC_CLIENT_SECRET", "API_UPSTREAM", "PUBLIC_URL", "UI_SESSION_KEYS"]) {
      expect(got.some((p) => p.startsWith(name))).toBe(true);
    }
  });

  test("rejects short session keys, naming the entry", () => {
    expect(problems({ ...validEnv, UI_SESSION_KEYS: `${key},short` })).toEqual([
      "UI_SESSION_KEYS entry 2 is shorter than 32 bytes",
    ]);
  });

  test("derives distinct keys in order", () => {
    const cfg = loadConfig({ ...validEnv, UI_SESSION_KEYS: `${key}, ${"j".repeat(40)}` });
    expect(cfg.sessionKeys).toHaveLength(2);
    expect(cfg.sessionKeys[0]).not.toEqual(cfg.sessionKeys[1]);
  });

  test("requires https for PUBLIC_URL except on localhost", () => {
    expect(problems({ ...validEnv, PUBLIC_URL: "http://guardian.example.com" })).toHaveLength(1);
    expect(problems({ ...validEnv, PUBLIC_URL: "http://localhost:5173" })).toEqual([]);
  });

  test("rejects a PUBLIC_URL with a path", () => {
    expect(problems({ ...validEnv, PUBLIC_URL: "https://example.com/ui" })).toHaveLength(1);
  });

  test("rejects malformed URLs, TTLs and scopes", () => {
    expect(problems({ ...validEnv, API_UPSTREAM: "not a url" })).toHaveLength(1);
    expect(problems({ ...validEnv, UI_SESSION_TTL: "8 hours" })).toHaveLength(1);
    expect(problems({ ...validEnv, OIDC_SCOPES: "profile email" })).toHaveLength(1);
  });
});

describe("parseDuration", () => {
  test.each([
    ["8h", 28800],
    ["90m", 5400],
    ["1h30m", 5400],
    ["45s", 45],
  ])("%s", (s, want) => {
    expect(parseDuration(s)).toBe(want);
  });

  test.each(["", "8", "h", "8d", "1h 30m", "x8h"])("rejects %p", (s) => {
    expect(parseDuration(s)).toBeUndefined();
  });
});

describe("publicConfig", () => {
  test("exposes no secret, key or upstream", () => {
    const out = JSON.stringify(publicConfig(loadConfig(validEnv)));
    for (const secret of ["client-secret", key, "repo-guardian-api", "repo-guardian-ui"]) {
      expect(out).not.toContain(secret);
    }
  });
});
