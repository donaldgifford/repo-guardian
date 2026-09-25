import { Hono } from "hono";

import { mountAuth } from "./auth";
import { type Config, publicConfig } from "./config";
import { jsonLogger, type Logger } from "./log";
import { Oidc } from "./oidc";
import { mountProxy } from "./proxy";
import { sessionCookie } from "./session";

export interface AppDeps {
  config: Config;
  log?: Logger;
  // upstreamFetch replaces fetch for the API proxy in tests.
  upstreamFetch?: typeof fetch;
}

// createApp builds the BFF's routes; main.ts only binds it to a port.
export function createApp({ config, log = jsonLogger, upstreamFetch }: AppDeps): Hono {
  const app = new Hono();
  const oidc = new Oidc(config);
  const session = sessionCookie(config.sessionKeys);

  app.get("/healthz", (c) => c.text("ok"));
  app.get("/ui/config", (c) => c.json(publicConfig(config)));

  mountAuth(app, { config, oidc, session, log });
  mountProxy(app, { config, oidc, session, log, ...(upstreamFetch ? { fetch: upstreamFetch } : {}) });

  return app;
}
