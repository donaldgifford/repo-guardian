import { Hono } from "hono";

import { type Config, publicConfig } from "./config";

export interface AppDeps {
  config: Config;
}

// createApp builds the BFF's routes; main.ts only binds it to a port.
export function createApp({ config }: AppDeps): Hono {
  const app = new Hono();

  app.get("/healthz", (c) => c.text("ok"));
  app.get("/ui/config", (c) => c.json(publicConfig(config)));

  return app;
}
