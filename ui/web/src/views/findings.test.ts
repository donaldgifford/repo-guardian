import { expect, test } from "bun:test";
import { renderToStaticMarkup } from "react-dom/server";

import type { Schemas } from "../api/client";
import { NOT_ENABLED } from "../components/GatedPanel";
import { controlRules, defaultSort, findingColumns, parseFindingsSearch, resultsFilter } from "./findings";
import { evidenceGroups, findingKey, parseOpenKey, reassignedEvents, timelineEvents } from "./findings/drawer";

test("parseFindingsSearch keeps the /results filters, drops the default sort and junk", () => {
  expect(parseFindingsSearch({})).toEqual({});
  expect(parseFindingsSearch({ control: "codeowners", rule: "present", org: "acme", decision: "org_added", pr_stale: "true", sort: "since_desc", open: "7/codeowners" })).toEqual({
    control: "codeowners",
    rule: "present",
    org: "acme",
    decision: "org_added",
    pr_stale: true,
    sort: "since_desc",
    open: "7/codeowners",
  });
  expect(parseFindingsSearch({ pr_stale: false, sort: defaultSort, control: "", rule: 3, bogus: "x" })).toEqual({ pr_stale: false });
  expect(parseFindingsSearch({ sort: "random" })).toEqual({});
});

test("resultsFilter is always the non-compliant bucket, scoped by org when no chip names one", () => {
  expect(resultsFilter({}, undefined)).toEqual({ bucket: "non_compliant", sort: "since_asc" });
  expect(resultsFilter({ open: "1/x", sort: "pr_age", pr_stale: true }, "acme")).toEqual({ bucket: "non_compliant", sort: "pr_age", org: "acme", pr_stale: true });
  expect(resultsFilter({ org: "globex", control: "c" }, "acme")).toEqual({ bucket: "non_compliant", sort: "since_asc", control: "c", org: "globex" });
});

test("parseOpenKey reads <repository_id>/<control> and rejects the rest", () => {
  expect(parseOpenKey("12/codeowners")).toEqual({ repositoryId: 12, control: "codeowners" });
  expect(parseOpenKey("12/with/slash")).toEqual({ repositoryId: 12, control: "with/slash" });
  for (const bad of [undefined, "", "12", "/codeowners", "0/codeowners", "x/codeowners", "12/", "1.5/c"]) {
    expect(parseOpenKey(bad)).toBeUndefined();
  }
  expect(parseOpenKey(findingKey({ repository_id: 3, control_id: "labels" }))).toEqual({ repositoryId: 3, control: "labels" });
});

test("controlRules is the failing-rule union for the chosen control", () => {
  const row = (control_id: string, failing_rules: string[]): Schemas["ResultRow"] =>
    ({ repository_id: 1, org: "acme", repository: "r", control_id, control_version: 1, decision: "baseline", status: "fail", bucket: "non_compliant", failing_rules, last_changed_at: "2026-01-01T00:00:00Z" }) as Schemas["ResultRow"];
  const rows = [row("codeowners", ["present", "valid"]), row("codeowners", ["valid"]), row("labels", ["sync"])];
  expect(controlRules(rows, "codeowners")).toEqual(["present", "valid"]);
  expect(controlRules(rows, "labels")).toEqual(["sync"]);
  expect(controlRules(rows, undefined)).toEqual([]);
});

test("timelineEvents keeps the control's events and the repository's parks and renames", () => {
  const ev = (p: Partial<Schemas["Event"]>): Schemas["Event"] => ({ id: 1, source: "result", occurred_at: "2026-01-01T00:00:00Z", ...p }) as Schemas["Event"];
  const events = [
    ev({ id: 1, control_id: "codeowners", to_status: "fail" }),
    ev({ id: 2, control_id: "labels", to_status: "pass" }),
    ev({ id: 3, source: "repository", kind: "renamed" }),
    ev({ id: 4, source: "repository", kind: "discovered" }),
    ev({ id: 5, source: "remediation", control_id: "codeowners", kind: "pr_opened" }),
  ];
  expect(timelineEvents(events, "codeowners").map((e) => e.id)).toEqual([1, 3, 5]);
  expect(timelineEvents(events, "codeowners", 1).map((e) => e.id)).toEqual([1]);
});

test("the Remediation column is a gated dash without the App and the badge with it", () => {
  const row = { repository_id: 1, org: "acme", repository: "r", control_id: "c", control_version: 1, decision: "baseline", status: "fail", bucket: "non_compliant", failing_rules: ["x"], last_changed_at: "2026-01-01T00:00:00Z", remediation: { kind: "pull_request", state: "open", pr_number: 7, pr_url: "https://github.com/acme/r/pull/7", created_at: "2026-01-01T00:00:00Z", age_days: 3 } } as Schemas["ResultRow"];
  const cell = (enabled: boolean | undefined) => renderToStaticMarkup(findingColumns(enabled).find((c) => c.key === "remediation")?.cell(row));
  expect(cell(false)).toContain(`title="${NOT_ENABLED}"`);
  expect(cell(false)).toContain('data-gated="not_enabled"');
  expect(cell(false)).not.toContain("pull/7");
  expect(cell(true)).toContain("pull/7");
  expect(cell(true)).toContain("pr open");
  // Unknown (status not loaded yet) shows the data rather than a dash.
  expect(cell(undefined)).toContain("pull/7");
});

test("reassignedEvents marks a first status that follows an earlier one", () => {
  const ev = (p: Partial<Schemas["Event"]>): Schemas["Event"] => ({ source: "result", occurred_at: "2026-01-01T00:00:00Z", control_id: "catalog_info", ...p }) as Schemas["Event"];
  // Newest first: a policy change re-assigned the control (ids 3 and 4)
  // after its first evaluation (ids 1 and 2); id 5 is another control.
  const events = [
    ev({ id: 4, rule_id: "exists", to_status: "fail" }),
    ev({ id: 3, to_status: "non_compliant" }),
    ev({ id: 5, control_id: "codeowners", to_status: "non_compliant" }),
    ev({ id: 2, rule_id: "exists", to_status: "fail" }),
    ev({ id: 1, to_status: "non_compliant" }),
  ];
  expect([...reassignedEvents(events)].sort()).toEqual([3, 4]);
});

test("evidenceGroups folds rules with the same evidence, in order", () => {
  const missing = { evidence_kind: "file_missing", evidence_version: 1, locations: "catalog-info.yaml" } as unknown as Schemas["Evidence"];
  const other = { evidence_kind: "owner_unset", evidence_version: 1 } as unknown as Schemas["Evidence"];
  const rule = (rule_id: string, evidence: Schemas["Evidence"]) => ({ rule_id, status: "fail", remediate: false, evidence }) as unknown as Schemas["RuleResult"];
  const groups = evidenceGroups([rule("exists", missing), rule("owner-set", other), rule("kind", missing)]);
  expect(groups.map((g) => g.rules)).toEqual([["exists", "kind"], ["owner-set"]]);
});
