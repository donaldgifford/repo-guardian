import { Hono } from "hono";

import { mountAuth } from "./auth";
import { type Config, publicConfig } from "./config";
import { jsonLogger, type Logger } from "./log";
import { Oidc } from "./oidc";
import { sessionCookie } from "./session";

export interface AppDeps {
  config: Config;
  log?: Logger;
}

// createApp builds the BFF's routes; main.ts only binds it to a port.
export function createApp({ config, log = jsonLogger }: AppDeps): Hono {
  const app = new Hono();
  const oidc = new Oidc(config);
  const session = sessionCookie(config.sessionKeys);

  app.get("/healthz", (c) => c.text("ok"));
  app.get("/ui/config", (c) => c.json(publicConfig(config)));

  mountAuth(app, { config, oidc, session, log });

  return app;
}
