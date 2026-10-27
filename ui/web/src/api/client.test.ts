import { expect, test } from "bun:test";

import { loginPath, toProblem } from "./client";

test("toProblem keeps an RFC 9457 body", () => {
  expect(toProblem({ type: "about:blank", title: "Not Found", status: 404, detail: "no such org", request_id: "r1", extra: 1 }, 404)).toEqual({
    type: "about:blank",
    title: "Not Found",
    status: 404,
    detail: "no such org",
    request_id: "r1",
  });
});

test("toProblem builds one from the status when the body is not a problem", () => {
  expect(toProblem("<html>bad gateway</html>", 502, "Bad Gateway")).toEqual({ type: "about:blank", title: "Bad Gateway", status: 502 });
  expect(toProblem(undefined, 500)).toEqual({ type: "about:blank", title: "HTTP 500", status: 500 });
});

test("loginPath returns to the current page", () => {
  expect(loginPath({ pathname: "/findings", search: "?org=acme" })).toBe("/auth/login?return_to=%2Ffindings%3Forg%3Dacme");
});
