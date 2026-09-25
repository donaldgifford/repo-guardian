import { Hono } from "hono";

// createApp builds the BFF's routes. Later phases add config, sessions,
// OIDC, the API proxy and static serving; main.ts only binds it to a port.
export function createApp(): Hono {
  const app = new Hono();

  app.get("/healthz", (c) => c.text("ok"));

  return app;
}
