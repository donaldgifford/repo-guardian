import { describe, expect, test } from "bun:test";

import { describeEvidence } from "./evidence";

const ev = (reason: string, fields: Record<string, unknown> = {}) =>
  // The API's Evidence is a oneOf; tests build it loosely on purpose.
  ({ reason, evidence: { reason, ...fields } }) as unknown as Parameters<typeof describeEvidence>[0];

describe("describeEvidence", () => {
  test.each([
    ["file_missing", { paths_checked: ["CODEOWNERS", ".github/CODEOWNERS"] }, "file missing", ["checked CODEOWNERS", "checked .github/CODEOWNERS"]],
    ["assertion_failed", { path: "renovate.json", message: "extends must include config:base" }, "assertion failed in renovate.json", ["extends must include config:base"]],
    ["content_differs", { path: ".github/dependabot.yml", comparison: "yaml" }, ".github/dependabot.yml differs from the template", ["compared as yaml"]],
    ["forbidden_present", { path: ".travis.yml" }, "forbidden file present: .travis.yml", []],
    ["setting_mismatch", { property: "delete_branch_on_merge", expected: "true", actual: "false" }, "setting delete_branch_on_merge", ["expected true", "actual false"]],
    ["ruleset_missing", { branch: "main" }, "no ruleset covers main", []],
    ["ruleset_mismatch", { branch: "main", ruleset_id: 7, mismatches: ["required_approvals 1 != 2"] }, "ruleset on main differs", ["required_approvals 1 != 2"]],
    ["out_of_scope_rule", {}, "org outside the rule's scope", []],
    ["ignored_rule", { pattern: "acme/legacy-*" }, "ignored by the rule", ["pattern acme/legacy-*"]],
    ["gate_closed", { referee: "renovate" }, "gate closed: renovate not satisfied", []],
    ["branch_missing", { branch: "release" }, "branch release does not exist", []],
    ["gate_error", { referee: "renovate", error: "GET 502" }, "gate error evaluating renovate", ["GET 502"]],
    ["migrated_from_v1", { v1_actionable_since: "2026-01-01T00:00:00Z" }, "last checked by v1; details on next check", []],
  ])("%s", (reason, fields, headline, lines) => {
    expect(describeEvidence(ev(reason, fields))).toEqual({ reason, headline, lines });
  });

  test("compliant findings have no evidence to show", () => {
    expect(describeEvidence({})).toBeUndefined();
  });

  test("an unknown reason shows only its code", () => {
    expect(describeEvidence(ev("quantum_drift", { anything: "<b>x</b>" }))).toEqual({ reason: "quantum_drift", headline: "quantum_drift", lines: [] });
  });

  test("evidence in a shape the renderer does not know shows only the code", () => {
    expect(describeEvidence(ev("assertion_failed", { path: "renovate.json", message: { html: "<b>" } }))).toEqual({
      reason: "assertion_failed",
      headline: "assertion_failed",
      lines: [],
    });
    expect(describeEvidence(ev("file_missing", { paths_checked: "CODEOWNERS" }))?.headline).toBe("file_missing");
  });

  test("evidence whose reason disagrees with the finding shows only the code", () => {
    const f = { reason: "gate_closed", evidence: { reason: "gate_error", referee: "x", error: "y" } } as unknown as Parameters<typeof describeEvidence>[0];
    expect(describeEvidence(f)?.headline).toBe("gate_closed");
  });

  test("an inherited property name is not a renderer", () => {
    expect(describeEvidence(ev("constructor"))?.headline).toBe("constructor");
    expect(describeEvidence(ev("__proto__"))?.headline).toBe("__proto__");
  });
});
