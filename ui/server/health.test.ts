import { afterAll, beforeAll, describe, expect, test } from "bun:test";
import { Hono } from "hono";

import { loadConfig } from "./config";
import { mountHealth } from "./health";
import { discardLogger } from "./log";
import { Oidc } from "./oidc";
import { type MockIssuer, startMockIssuer } from "./testing/mock-issuer";
import { validEnv } from "./testenv";

let issuer: MockIssuer;

beforeAll(async () => {
  issuer = await startMockIssuer();
});

afterAll(() => issuer.stop());

function setup(opts: { issuerUrl?: string; upstream?: () => Response } = {}) {
  const config = loadConfig({ ...validEnv, OIDC_ISSUER: opts.issuerUrl ?? issuer.url.toString() });
  const probes: string[] = [];
  let clock = 1_000_000;
  const app = new Hono();
  mountHealth(app, {
    config,
    oidc: new Oidc(config),
    log: discardLogger,
    now: () => clock,
    fetch: async (url) => {
      probes.push(url.toString());
      return (opts.upstream ?? (() => new Response("ok")))();
    },
  });
  return { app, probes, tick: (ms: number) => (clock += ms) };
}

describe("health", () => {
  test("/healthz is always ok", async () => {
    const { app } = setup({ issuerUrl: "http://127.0.0.1:1/realm" });
    expect((await app.request("/healthz")).status).toBe(200);
  });

  test("/readyz is ready once discovery loads and the upstream answers", async () => {
    const { app, probes } = setup();
    const res = await app.request("/readyz");
    expect(res.status).toBe(200);
    expect(await res.json()).toEqual({ issuer: true, upstream: true });
    expect(probes).toEqual(["http://repo-guardian-api/healthz"]);
  });

  test("/readyz is not ready while the issuer is unreachable", async () => {
    const { app } = setup({ issuerUrl: "http://127.0.0.1:1/realm" });
    const res = await app.request("/readyz");
    expect(res.status).toBe(503);
    expect(await res.json()).toEqual({ issuer: false, upstream: true });
  });

  test("/readyz is not ready while the upstream fails", async () => {
    const { app } = setup({ upstream: () => new Response("down", { status: 503 }) });
    const res = await app.request("/readyz");
    expect(res.status).toBe(503);
    expect(await res.json()).toEqual({ issuer: true, upstream: false });
  });

  test("an upstream that throws is not ready", async () => {
    const { app } = setup({
      upstream: () => {
        throw new TypeError("connection refused");
      },
    });
    expect((await app.request("/readyz")).status).toBe(503);
  });

  test("the upstream probe is cached for 10s", async () => {
    const { app, probes, tick } = setup();
    await app.request("/readyz");
    tick(9_999);
    await app.request("/readyz");
    expect(probes).toHaveLength(1);
    tick(1);
    await app.request("/readyz");
    expect(probes).toHaveLength(2);
  });
});
