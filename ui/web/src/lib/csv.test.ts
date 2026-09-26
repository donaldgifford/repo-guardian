import { expect, test } from "bun:test";

import type { Schemas } from "../api/client";
import { csvCell, findingsCsv } from "./csv";

test.each([
  ["plain", "plain"],
  ["a,b", '"a,b"'],
  ['say "hi"', '"say ""hi"""'],
  ["two\nlines", '"two\nlines"'],
  ["=HYPERLINK(\"x\")", "\"'=HYPERLINK(\"\"x\"\")\""],
  ["+1", "'+1"],
  ["-1", "'-1"],
  ["@SUM(A1)", "'@SUM(A1)"],
  [undefined, ""],
  [42, "42"],
  [false, "false"],
])("csvCell(%p)", (v, want) => {
  expect(csvCell(v)).toBe(want);
});

test("findingsCsv writes a header and one row per finding", () => {
  const f: Schemas["Finding"] = {
    repository_id: 1,
    org: "acme",
    repository: "web",
    rule_kind: "file",
    rule_name: "renovate",
    status: "non_compliant",
    reason: "file_missing",
    remediation: "pr_open",
    status_since: "2026-09-01T00:00:00Z",
    last_evaluated_at: "2026-09-25T00:00:00Z",
    pr_stale: false,
    pr: { number: 7, url: "https://github.com/acme/web/pull/7" },
  };
  expect(findingsCsv([f])).toBe(
    "org,repository,rule_kind,rule_name,status,reason,remediation,status_since,pr_number,pr_stale\r\n" +
      "acme,web,file,renovate,non_compliant,file_missing,pr_open,2026-09-01T00:00:00Z,7,false\r\n",
  );
});
