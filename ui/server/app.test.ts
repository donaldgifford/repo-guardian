import { describe, expect, test } from "bun:test";

import { createApp } from "./app";
import { loadConfig } from "./config";
import { validEnv } from "./testenv";

const app = () => createApp({ config: loadConfig(validEnv) });

describe("app", () => {
  test("serves /healthz", async () => {
    const res = await app().request("/healthz");
    expect(res.status).toBe(200);
    expect(await res.text()).toBe("ok");
  });

  test("serves display-safe /ui/config", async () => {
    const res = await app().request("/ui/config");
    expect(res.status).toBe(200);
    expect(await res.json()).toEqual({
      issuer: "https://idp.example.com",
      public_url: "https://guardian.example.com",
      session_ttl_seconds: 28800,
      github_host: "github.com",
    });
  });
});
