import { fileURLToPath } from "node:url";

import { Hono } from "hono";

import { mountAuth } from "./auth";
import { type Config, publicConfig } from "./config";
import { mountHealth } from "./health";
import { jsonLogger, type Logger } from "./log";
import { Oidc } from "./oidc";
import { mountProxy, type UpstreamFetch } from "./proxy";
import { sessionCookie } from "./session";
import { mountStatic, securityHeaders } from "./static";

export interface AppDeps {
  config: Config;
  log?: Logger;
  // upstreamFetch replaces fetch for the API proxy in tests.
  upstreamFetch?: UpstreamFetch;
  // staticDir is the SPA build; it defaults to ui/dist.
  staticDir?: string;
}

const defaultStaticDir = fileURLToPath(new URL("../dist", import.meta.url));

// createApp builds the BFF's routes; main.ts only binds it to a port.
export function createApp({ config, log = jsonLogger, upstreamFetch, staticDir = defaultStaticDir }: AppDeps): Hono {
  const app = new Hono();
  const oidc = new Oidc(config);
  const session = sessionCookie(config.sessionKeys);

  app.use(securityHeaders);

  mountHealth(app, { config, oidc, log, fetch: upstreamFetch ?? fetch });
  app.get("/ui/config", (c) => c.json(publicConfig(config)));

  mountAuth(app, { config, oidc, session, log });
  mountProxy(app, { config, oidc, session, log, ...(upstreamFetch ? { fetch: upstreamFetch } : {}) });
  mountStatic(app, { dir: staticDir, session });

  return app;
}
