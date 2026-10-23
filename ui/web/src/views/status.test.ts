// Register the DOM first: the view imports primitives that decide at
// module load whether a document exists.
import { withDOM } from "../../testing/dom";

import { expect, test } from "bun:test";

import { renderToStaticMarkup } from "react-dom/server";

import { Changelog, changelogShown, OperationsStrip, releasesURL, rolloutLabel, statusHeadline, type StatusTone } from "./status";

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

test("the changelog card shows the newest five, marks the running version and links to all releases (IMPL-0032 7.5)", () => {
  const entries = Array.from({ length: 7 }, (_, i) => ({ version: `2.0.0-rc.${20 - i}`, date: `2026-10-${23 - i}`, title: `release ${i}`, summary: `summary ${i}`, tags: ["fixed", "ui"] }));
  const html = renderToStaticMarkup(Changelog({ bundle: { running: "2.0.0-rc.20", entries } }));
  expect(html.match(/data-version=/g)).toHaveLength(changelogShown);
  expect(html).toContain('data-version="2.0.0-rc.20" data-running="true"');
  expect(html).toContain('data-version="2.0.0-rc.19" data-running="false"');
  expect(html).not.toContain("2.0.0-rc.15");
  expect((html.match(/>running</g) ?? []).length).toBe(1);
  expect(html).toContain(`href="${releasesURL}"`);
  // The bundled changelog: the running version is the newest entry, as the gate requires.
  const bundled = renderToStaticMarkup(Changelog({}));
  expect(bundled).toContain('data-running="true"');
});
