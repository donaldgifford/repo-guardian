import { expect, test } from "bun:test";

import { patchSearch } from "./search";

test("patchSearch sets values and drops emptied keys", () => {
  const prev: Record<string, unknown> = { org: "acme", status: "non_compliant" };
  expect(patchSearch(prev, { status: "", rule: "renovate" })).toEqual({ org: "acme", rule: "renovate" });
  expect(patchSearch<Record<string, unknown>>({ org: "acme" }, { org: undefined })).toEqual({});
  expect(patchSearch({}, { pr_stale: false })).toEqual({ pr_stale: false });
});
