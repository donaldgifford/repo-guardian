import { expect, test } from "bun:test";

import { findingKey, parseFindingsSearch, resultCount } from "./findings";

test("findingKey is repository/kind/name, the same shape ?open= carries", () => {
  expect(findingKey({ repository_id: 1, rule_kind: "file", rule_name: "renovate" })).toBe("1/file/renovate");
});

test("parseFindingsSearch keeps every documented filter and open, and types pr_stale", () => {
  expect(parseFindingsSearch({ status: "non_compliant", rule: "renovate", pr_stale: "true", open: "1/file/renovate", junk: "x" })).toEqual({
    status: "non_compliant",
    rule: "renovate",
    pr_stale: true,
    open: "1/file/renovate",
  });
  expect(parseFindingsSearch({ pr_stale: false, open: "" })).toEqual({ pr_stale: false });
});

test.each([
  [3, "non_compliant", 12, false, "3 of 12 findings"],
  [3, "non_compliant", undefined, false, "3 findings"],
  [1, undefined, 12, false, "1 finding"],
  [50, "compliant", 12, true, "50 findings loaded, more available"],
])("resultCount(%p, %p, %p, %p)", (loaded, status, total, more, want) => {
  expect(resultCount(loaded, status, total, more)).toBe(want);
});
