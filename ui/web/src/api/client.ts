import createClient from "openapi-fetch";

import type { components, paths } from "./schema.gen";

export type Schemas = components["schemas"];
export type Problem = Schemas["Problem"];

// api calls the same-origin BFF, which adds the session's token. The
// browser never holds one.
export const api = createClient<paths>({ baseUrl: "/api/v1" });

// ProblemError carries an RFC 9457 problem to the error boundary.
export class ProblemError extends Error {
  constructor(readonly problem: Problem) {
    super(problem.title);
    this.name = "ProblemError";
  }
}

// toProblem reads an error body as a problem, or builds one from the
// status when the body is not one (a proxy's 502, say).
export function toProblem(body: unknown, status: number, statusText = ""): Problem {
  if (typeof body === "object" && body !== null) {
    const b = body as Record<string, unknown>;
    if (typeof b.title === "string" && typeof b.status === "number") {
      const p: Problem = { type: typeof b.type === "string" ? b.type : "about:blank", title: b.title, status: b.status };
      if (typeof b.detail === "string") p.detail = b.detail;
      if (typeof b.instance === "string") p.instance = b.instance;
      if (typeof b.request_id === "string") p.request_id = b.request_id;
      return p;
    }
  }
  return { type: "about:blank", title: statusText || `HTTP ${status}`, status };
}

// loginPath is where a lapsed session is sent, returning to the page.
export function loginPath(location: { pathname: string; search: string }): string {
  return `/auth/login?return_to=${encodeURIComponent(location.pathname + location.search)}`;
}

// unwrap returns a call's data or throws its problem. A 401 means the
// session lapsed: the page reloads through login and comes back.
export async function unwrap<T>(call: Promise<{ data?: T; error?: unknown; response: Response }>): Promise<T> {
  const { data, error, response } = await call;
  if (response.ok && data !== undefined) {
    return data;
  }
  if (response.status === 401) {
    window.location.assign(loginPath(window.location));
  }
  throw new ProblemError(toProblem(error, response.status, response.statusText));
}
