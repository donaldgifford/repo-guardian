import { resolve, sep } from "node:path";

import type { Hono, MiddlewareHandler } from "hono";

import type { SealedCookie, Session } from "./session";

// contentSecurityPolicy is DESIGN-0027's: the SPA loads nothing and
// talks to nothing but its own origin, and is never framed.
export const contentSecurityPolicy = "default-src 'self'; connect-src 'self'; frame-ancestors 'none'; script-src 'self'";

// securityHeaders sets the headers every response carries, API proxy
// included.
export const securityHeaders: MiddlewareHandler = async (c, next) => {
  await next();
  c.header("content-security-policy", contentSecurityPolicy);
  c.header("x-content-type-options", "nosniff");
  c.header("referrer-policy", "no-referrer");
};

// publicAppPaths are SPA routes rendered without a session.
const publicAppPaths = new Set(["/status"]);

// serverPrefixes are owned by server routes; the SPA fallback never
// answers under them, so a typo there is a 404, not the app shell.
const serverPrefixes = ["/api/", "/auth/", "/ui/", "/assets/"];

export interface StaticDeps {
  // dir is the Vite build output (ui/dist).
  dir: string;
  session: SealedCookie<Session>;
}

// mountStatic serves the SPA build. Hashed assets are cached for a year;
// index.html is never cached, so a deploy is picked up on the next load.
// Any other GET renders the app shell, after a login redirect for every
// path but the public ones. Mount it last: it is the catch-all.
export function mountStatic(app: Hono, { dir, session }: StaticDeps): void {
  const root = resolve(dir);

  app.get("/assets/*", async (c) => {
    const file = await within(root, c.req.path);
    if (!file) {
      return c.notFound();
    }
    c.header("cache-control", "public, max-age=31536000, immutable");
    return c.body(file.stream(), 200, { "content-type": file.type });
  });

  app.get("*", async (c) => {
    const path = c.req.path;
    if (serverPrefixes.some((p) => path.startsWith(p))) {
      return c.notFound();
    }

    // Root-level build files (favicon, robots.txt) are served as-is,
    // briefly cached because their names carry no hash.
    if (path !== "/" && path !== "/index.html") {
      const file = await within(root, path);
      if (file) {
        c.header("cache-control", "public, max-age=3600");
        return c.body(file.stream(), 200, { "content-type": file.type });
      }
    }

    if (!publicAppPaths.has(path) && !(await session.read(c))) {
      const url = new URL(c.req.url);
      return c.redirect(`/auth/login?return_to=${encodeURIComponent(url.pathname + url.search)}`, 302);
    }

    const index = Bun.file(resolve(root, "index.html"));
    if (!(await index.exists())) {
      return c.text("the UI build is missing", 503);
    }
    c.header("cache-control", "no-cache");
    return c.body(index.stream(), 200, { "content-type": "text/html; charset=utf-8" });
  });
}

// within returns the file at urlPath under root, or undefined when it is
// missing, is a directory or resolves outside root.
async function within(root: string, urlPath: string): Promise<ReturnType<typeof Bun.file> | undefined> {
  let decoded: string;
  try {
    decoded = decodeURIComponent(urlPath);
  } catch {
    return undefined;
  }
  if (decoded.includes("\0")) {
    return undefined;
  }

  const full = resolve(root, `.${decoded}`);
  if (!full.startsWith(root + sep)) {
    return undefined;
  }

  const file = Bun.file(full);
  return (await file.exists()) ? file : undefined;
}
