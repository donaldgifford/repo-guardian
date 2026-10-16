import { expect, test } from "bun:test";

import type { Schemas } from "../api/client";
import { describeEvidence } from "./evidence";

test("framework evidence is headed by its reason", () => {
  const e = { evidence_kind: "framework.unknown", evidence_version: 1, reason: "read_failed" } as Schemas["Evidence"];
  expect(describeEvidence(e)).toEqual({ headline: "read_failed", lines: [] });
});

test("control evidence lists its scalar fields as text, sorted, skipping objects", () => {
  const e = {
    evidence_kind: "file.exists",
    evidence_version: 1,
    path: "<img src=x onerror=alert(1)>",
    present: false,
    checked: ["CODEOWNERS", ".github/CODEOWNERS"],
    nested: { a: 1 },
  } as Schemas["Evidence"];
  expect(describeEvidence(e)).toEqual({
    headline: "file.exists",
    lines: ["checked: CODEOWNERS, .github/CODEOWNERS", "path: <img src=x onerror=alert(1)>", "present: false"],
  });
});
