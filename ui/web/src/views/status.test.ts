// Register the DOM first: the view imports primitives that decide at
// module load whether a document exists.
import { withDOM } from "../../testing/dom";

import { expect, test } from "bun:test";

import { renderToStaticMarkup } from "react-dom/server";

import { OperationsStrip, rolloutLabel, statusHeadline, type StatusTone } from "./status";

withDOM();

test.each<[string, string, StatusTone]>([
  ["operational", "All systems operational", "ok"],
  ["degraded", "Degraded performance", "warn"],
  ["down", "Service disruption", "bad"],
  ["on_fire", "Service disruption", "bad"],
  ["unknown", "Status not yet available", "mute"],
])("statusHeadline(%p)", (state, headline, tone) => {
  expect(statusHeadline(state)).toEqual({ headline, tone });
});

test("rolloutLabel maps the closed enum onto fixed text", () => {
  const ops = { evaluated_24h: 3, overdue: 0, tracked: 4, remediation_backlog: 0 };
  expect(rolloutLabel({ ...ops, rollout_state: "none" })).toEqual({ text: "—" });
  expect(rolloutLabel({ ...ops, rollout_state: "in_progress" })).toEqual({ text: "in progress" });
  expect(rolloutLabel({ ...ops, rollout_state: "complete", rollout_completed_at: "2026-09-01T12:00:00Z" })).toEqual({
    text: "complete",
    at: "2026-09-01T12:00:00Z",
  });
});

test("the operations strip shows the remediation backlog only with the Remediation App (IMPL-0032 6.7)", () => {
  const ops = { evaluated_24h: 3, overdue: 0, tracked: 4, remediation_backlog: 7, rollout_state: "none" as const };
  const without = renderToStaticMarkup(OperationsStrip({ ops, remediation: false }));
  expect(without).not.toContain("Remediation backlog");
  expect(without).toContain("md:grid-cols-4");
  const withApp = renderToStaticMarkup(OperationsStrip({ ops, remediation: true }));
  expect(withApp).toContain("Remediation backlog");
  expect(withApp).toContain(">7<");
  expect(withApp).toContain("md:grid-cols-5");
});
