import { expect, test } from "bun:test";
import { renderToString } from "react-dom/server";

import { ProblemError } from "../api/client";
import { ProblemView } from "./problem";

test("a problem reads as status and title, with its detail and request id", () => {
  const html = renderToString(<ProblemView error={new ProblemError({ type: "about:blank", title: "Not Found", status: 404, detail: "no org globex", request_id: "req-42" })} />);
  expect(html).toContain('role="alert"');
  expect(html).toContain("404 · Not Found");
  expect(html).toContain("no org globex");
  expect(html).toMatch(/request (<!-- -->)?req-42/);
});

test("a problem without a request id shows none, and any other error is something went wrong", () => {
  expect(renderToString(<ProblemView error={new ProblemError({ type: "about:blank", title: "Bad Request", status: 400 })} />)).not.toContain("request ");
  const html = renderToString(<ProblemView error={new Error("boom")} />);
  expect(html).toContain("Something went wrong");
  expect(html).toContain("boom");
});
