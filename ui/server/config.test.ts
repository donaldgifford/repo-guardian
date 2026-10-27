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
    expect(cfg.oidc.tokenAuthMethod).toBe("client_secret_post");
    expect(cfg.oidc.authorizationParams).toEqual({});
  });

  test("accepts client_secret_basic and refuses any other token method", () => {
    expect(loadConfig({ ...validEnv, OIDC_TOKEN_AUTH_METHOD: "client_secret_basic" }).oidc.tokenAuthMethod).toBe(
      "client_secret_basic",
    );
    expect(problems({ ...validEnv, OIDC_TOKEN_AUTH_METHOD: "private_key_jwt" })).toEqual([
      'OIDC_TOKEN_AUTH_METHOD must be one of client_secret_post, client_secret_basic, got "private_key_jwt"',
    ]);
  });

  test("reads authorization params as a JSON object of strings", () => {
    const cfg = loadConfig({ ...validEnv, OIDC_AUTHORIZATION_PARAMS: '{"audience":"api://x","prompt":"login"}' });
    expect(cfg.oidc.authorizationParams).toEqual({ audience: "api://x", prompt: "login" });
  });

  test("refuses malformed authorization params", () => {
    for (const raw of ["not json", "[]", '"audience"', "null"]) {
      expect(problems({ ...validEnv, OIDC_AUTHORIZATION_PARAMS: raw })).toEqual([
        "OIDC_AUTHORIZATION_PARAMS must be a JSON object of string values",
      ]);
    }
    expect(problems({ ...validEnv, OIDC_AUTHORIZATION_PARAMS: '{"audience":1,"max_age":true}' })).toEqual([
      "OIDC_AUTHORIZATION_PARAMS values must be strings: audience, max_age",
    ]);
  });

  test("refuses every reserved authorization param, listing them all", () => {
    const reserved = [
      "redirect_uri",
      "scope",
      "state",
      "nonce",
      "code_challenge",
      "code_challenge_method",
      "response_type",
      "client_id",
    ];
    const raw = JSON.stringify(Object.fromEntries(reserved.map((k) => [k, "x"])));
    expect(problems({ ...validEnv, OIDC_AUTHORIZATION_PARAMS: raw })).toEqual([
      `OIDC_AUTHORIZATION_PARAMS may not set ${reserved.join(", ")}: the login flow sets them`,
    ]);
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
    expect(problems({ ...validEnv, GITHUB_HOST: "evil.example/path" })).toHaveLength(1);
  });

  test("UI_DOCS_URL is optional and must be https when set", () => {
    expect(loadConfig({ ...validEnv, UI_DOCS_URL: "" }).docsUrl).toBeUndefined();
    expect(loadConfig({ ...validEnv, UI_DOCS_URL: "https://docs.example.com/" }).docsUrl?.href).toBe("https://docs.example.com/");
    expect(problems({ ...validEnv, UI_DOCS_URL: "http://docs.example.com/" })).toEqual(['UI_DOCS_URL must be an https URL, got "http://docs.example.com/"']);
    expect(problems({ ...validEnv, UI_DOCS_URL: "docs.example.com" })).toEqual(['UI_DOCS_URL must be an absolute https URL, got "docs.example.com"']);
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

  test("carries docs_url only when UI_DOCS_URL is set", () => {
    expect(publicConfig(loadConfig(validEnv))).not.toHaveProperty("docs_url");
    expect(publicConfig(loadConfig({ ...validEnv, UI_DOCS_URL: "https://docs.example.com/repo-guardian/" })).docs_url).toBe("https://docs.example.com/repo-guardian/");
  });
});
