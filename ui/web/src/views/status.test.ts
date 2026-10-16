// Register the DOM first: the view imports primitives that decide at
// module load whether a document exists.
import { withDOM } from "../../testing/dom";

import { expect, test } from "bun:test";

import { statusHeadline, type StatusTone } from "./status";

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
