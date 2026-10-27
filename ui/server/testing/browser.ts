import type { Hono } from "hono";

// Browser drives a Hono app the way a browser would: it keeps cookies
// (honouring Max-Age=0 deletions) and sends them back on each request.
export class Browser {
  readonly jar = new Map<string, string>();

  constructor(
    private readonly app: Hono,
    private readonly origin = "https://guardian.example.com",
  ) {}

  async request(path: string, init: RequestInit = {}): Promise<Response> {
    const headers = new Headers(init.headers);
    if (this.jar.size > 0) {
      headers.set("cookie", [...this.jar].map(([k, v]) => `${k}=${v}`).join("; "));
    }

    const res = await this.app.request(new URL(path, this.origin).toString(), { ...init, headers });

    for (const sc of res.headers.getSetCookie()) {
      const [pair = ""] = sc.split(";");
      const eq = pair.indexOf("=");
      const name = pair.slice(0, eq);
      if (/Max-Age=0\b/i.test(sc)) {
        this.jar.delete(name);
      } else {
        this.jar.set(name, pair.slice(eq + 1));
      }
    }

    return res;
  }

  cookie(name: string): string | undefined {
    return this.jar.get(name);
  }
}
