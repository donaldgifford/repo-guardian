import { expect, test } from "bun:test";

import type { Schemas } from "../api/client";
import { buildTimeline, TIMELINE_CAP } from "./timeline";

const now = new Date("2026-10-16T12:00:00Z");

const events: Schemas["Event"][] = [
  { source: "finding", id: 1, occurred_at: "2026-10-01T00:00:00Z", rule_kind: "file", rule_name: "renovate", from_status: "compliant", to_status: "non_compliant", to_reason: "assertion_failed", to_remediation: "pr_open" },
  { source: "finding", id: 2, occurred_at: "2026-10-02T00:00:00Z", rule_kind: "file", rule_name: "dependabot", from_status: "compliant", to_status: "not_applicable", to_reason: "gate_closed" },
  { source: "repository", id: 3, occurred_at: "2026-09-01T00:00:00Z", kind: "discovered", detail: {} },
  { source: "repository", id: 4, occurred_at: "2026-10-03T00:00:00Z", kind: "renamed", detail: { from: "web-old", to: "web" } as never },
];

const checks: Schemas["Check"][] = [
  { id: 10, trigger: "schedule", outcome: "success", policy_version: "v", started_at: "2026-09-20T00:00:00Z" },
  { id: 11, trigger: "push", outcome: "error", policy_version: "v", started_at: "2026-10-05T00:00:00Z", error: "502" },
  { id: 12, trigger: "schedule", outcome: "success", policy_version: "v", started_at: "2026-10-10T00:00:00Z" },
];

test("a finding timeline keeps its rule's events, the four repository kinds and checks since status_since, oldest first", () => {
  const t = buildTimeline({ events, checks, rule: { kind: "file", name: "renovate" }, since: "2026-10-01T00:00:00Z", now });
  expect(t.nodes.map((n) => n.key)).toEqual(["finding/1", "repository/4", "check/11", "check/12"]);
  expect(t.nodes[0]?.label).toBe("renovate: compliant → non compliant");
  expect(t.nodes[0]?.detail).toBe("assertion_failed · pr_open");
  expect(t.nodes[1]?.detail).toBe("from web-old · to web");
  expect(t.nodes[2]?.label).toBe("re-checked · error");
  expect(t.nodes[2]?.detail).toBe("502");
  expect(t.truncated).toBe(false);
});

test("an open PR synthesises a still-open node last, with its age", () => {
  const t = buildTimeline({ events, checks, rule: { kind: "file", name: "renovate" }, since: "2026-10-01T00:00:00Z", pr: { number: 412, created_at: "2026-10-07T12:00:00Z", open: true }, now });
  const last = t.nodes[t.nodes.length - 1];
  expect(last?.label).toBe("PR #412 still open (9d)");
  expect(last?.tone).toBe("accent");
  const closed = buildTimeline({ events, checks, pr: { number: 412, created_at: "2026-10-07T12:00:00Z", open: false }, now });
  expect(closed.nodes.some((n) => n.source === "pr")).toBe(false);
});

test("without a rule every event shows, including discovered", () => {
  const t = buildTimeline({ events, checks, now });
  expect(t.nodes.map((n) => n.key)).toEqual(["repository/3", "check/10", "finding/1", "finding/2", "repository/4", "check/11", "check/12"]);
});

test("the list is capped at the latest 20 and says so", () => {
  const many: Schemas["Check"][] = Array.from({ length: 25 }, (_, i) => ({
    id: 100 + i,
    trigger: "schedule",
    outcome: "success",
    policy_version: "v",
    started_at: new Date(Date.UTC(2026, 9, 1, i)).toISOString(),
  }));
  const t = buildTimeline({ events: [], checks: many, now });
  expect(t.nodes).toHaveLength(TIMELINE_CAP);
  expect(t.total).toBe(25);
  expect(t.truncated).toBe(true);
  expect(t.nodes[0]?.key).toBe("check/105");
  expect(t.nodes[t.nodes.length - 1]?.key).toBe("check/124");
});
