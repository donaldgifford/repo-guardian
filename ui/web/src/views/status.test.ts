// Register the DOM first: the view imports primitives that decide at
// module load whether a document exists.
import { withDOM } from "../../testing/dom";

import { expect, test } from "bun:test";

import { rolloutLabel, statusHeadline, type StatusTone } from "./status";

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
  const ops = { evaluated_24h: 3, overdue: 0, tracked: 4 };
  expect(rolloutLabel({ ...ops, rollout_state: "none" })).toEqual({ text: "—" });
  expect(rolloutLabel({ ...ops, rollout_state: "in_progress" })).toEqual({ text: "in progress" });
  expect(rolloutLabel({ ...ops, rollout_state: "complete", rollout_completed_at: "2026-09-01T12:00:00Z" })).toEqual({
    text: "complete",
    at: "2026-09-01T12:00:00Z",
  });
});
