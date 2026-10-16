import { describe, expect, test } from "bun:test";

import type { Schemas } from "../api/client";
import { addPosture, emptyPosture, floorPercent, historySeries, postureSegments } from "./posture";

const posture = (over: Partial<Schemas["Posture"]>): Schemas["Posture"] => ({ ...emptyPosture(), ...over });

describe("floorPercent", () => {
  test.each([
    [2, 3, 66.6],
    [1, 1, 100],
    [0, 4, 0],
    [3, 0, null],
  ])("%d of %d", (num, den, want) => {
    expect(floorPercent(num, den)).toBe(want);
  });
});

test("addPosture recomputes both percentages from the sums", () => {
  const sum = addPosture(posture({ compliant: 1, assigned: 2, pending: 1 }), posture({ compliant: 1, non_compliant: 1, not_applicable: 1, assigned: 3 }));
  expect(sum).toMatchObject({ compliant: 2, non_compliant: 1, assigned: 5, percent: 66.6, coverage: 80 });
});

test("an empty posture has no data, never 100%", () => {
  expect(addPosture(emptyPosture(), emptyPosture())).toMatchObject({ percent: null, coverage: null });
});

test("postureSegments adds up to every bucket", () => {
  const p = posture({ compliant: 1, non_compliant: 2, pending: 3, stale: 4, unmeasurable_access_denied: 5, unmeasurable_conflict: 6, not_applicable: 7, excluded: 8 });
  expect(postureSegments(p).reduce((n, s) => n + s.value, 0)).toBe(36);
});

test("historySeries sums each snapshot over orgs and controls, oldest first", () => {
  const point = (at: string, compliant: number, non_compliant: number): Schemas["HistoryPoint"] => ({
    org: "acme",
    control_id: "codeowners",
    decision: "baseline",
    snapshot_at: at,
    compliance: posture({ compliant, non_compliant, assigned: compliant + non_compliant }),
  });
  const series = historySeries([point("2026-10-02T00:00:00Z", 1, 0), point("2026-10-01T00:00:00Z", 1, 1), point("2026-10-02T00:00:00Z", 0, 1)]);
  expect(series).toEqual([
    { at: "2026-10-01T00:00:00Z", percent: 50 },
    { at: "2026-10-02T00:00:00Z", percent: 50 },
  ]);
});
