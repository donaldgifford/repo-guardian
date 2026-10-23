import { expect, test } from "bun:test";
import { renderToStaticMarkup } from "react-dom/server";

import type { Schemas } from "../../api/client";
import { HoldBadge, RemediationStateBadge } from "../../components/badges";
import { HOLDS, holdSentence } from "../../lib/holds";
import { isStale, kpis, matchesKey, openSearch, parseRemediationKey, parseRemediationsSearch, remediationAge, remediationFilter, remediationKey, STATES, viewPreset } from "./model";

const now = new Date("2026-10-23T12:00:00Z");
const ago = (days: number) => new Date(now.getTime() - days * 86_400_000).toISOString();

// fixture is a Remediation as the API lists one.
function fixture(p: Partial<Schemas["Remediation"]>): Schemas["Remediation"] {
  return { id: 1, repository_id: 7, org: "acme", repository: "web", control_id: "renovate", kind: "pull_request", state: "open", intent_revision: "ir", created_at: ago(3), steps: [], ...p };
}

// heldResult is a branch-held control result as /remediations lists it
// under a hold filter: no id, no state.
function heldResult(p: Partial<Schemas["Remediation"]>): Schemas["Remediation"] {
  const { id: _id, state: _state, ...rest } = fixture(p);
  return rest;
}

test("parseRemediationsSearch keeps every filter, the non-default view and a positive age", () => {
  expect(parseRemediationsSearch({})).toEqual({});
  expect(parseRemediationsSearch({ view: "held", kind: "api", state: "failed", org: "acme", control: "labels", hold: "conflict", older_than: 30, open: "12" })).toEqual({
    view: "held",
    kind: "api",
    state: "failed",
    org: "acme",
    control: "labels",
    hold: "conflict",
    older_than: 30,
    open: 12,
  });
  expect(parseRemediationsSearch({ view: "all", older_than: "7" })).toEqual({ older_than: 7 });
  // A numeric id is a number either way, so the URL reads `open=12`; a held result's key stays a string.
  expect(parseRemediationsSearch({ open: 12 })).toEqual({ open: 12 });
  expect(parseRemediationsSearch({ open: "12" })).toEqual({ open: 12 });
  expect(parseRemediationsSearch({ open: "7/renovate" })).toEqual({ open: "7/renovate" });
  expect(parseRemediationsSearch({ open: 0 })).toEqual({});
  expect(openSearch(fixture({ id: 12 }))).toBe(12);
  expect(openSearch(heldResult({ hold: "pr_cap" }))).toBe("7/renovate");
  expect(parseRemediationsSearch({ view: "bogus", older_than: -1, kind: "" })).toEqual({});
});

test("the view presets map to /remediations and the chips and scope layer over them", () => {
  expect(viewPreset("all")).toEqual({});
  expect(viewPreset("open")).toEqual({ kind: "pull_request", state: "open" });
  expect(viewPreset("recommended")).toEqual({ state: "recommended" });
  expect(viewPreset("held")).toEqual({ hold: [...HOLDS] });
  expect(viewPreset("failed")).toEqual({ state: "failed" });
  expect(remediationFilter({ view: "open" }, "acme")).toEqual({ kind: "pull_request", state: "open", org: "acme" });
  expect(remediationFilter({ view: "held", hold: "conflict", org: "globex", older_than: 14 }, "acme")).toEqual({ hold: ["conflict"], org: "globex", older_than_days: 14 });
  expect(remediationFilter({ kind: "api", state: "applied", control: "labels" }, undefined)).toEqual({ kind: "api", state: "applied", control: "labels" });
});

test("the drawer key is the id, or repository/control for a held result", () => {
  expect(remediationKey(fixture({ id: 12 }))).toBe("12");
  const held = heldResult({ hold: "conflict", hold_since: ago(40) });
  expect(remediationKey(held)).toBe("7/renovate");
  expect(parseRemediationKey("12")).toEqual({ id: 12 });
  expect(parseRemediationKey("7/renovate")).toEqual({ repositoryId: 7, control: "renovate" });
  for (const bad of [undefined, "", "0", "/x", "x/y", "7/"]) expect(parseRemediationKey(bad)).toBeUndefined();
  expect(matchesKey(fixture({ id: 12 }), { id: 12 })).toBe(true);
  expect(matchesKey(fixture({ id: 12 }), { id: 13 })).toBe(false);
  expect(matchesKey(held, { repositoryId: 7, control: "renovate" })).toBe(true);
  expect(matchesKey(fixture({ id: 12 }), { repositoryId: 7, control: "renovate" })).toBe(false);
});

test("age is from created_at, or hold_since for a held result; an open PR is stale at 30 days", () => {
  expect(remediationAge(fixture({ created_at: ago(5) }), now)).toBe(5);
  expect(remediationAge(heldResult({ hold: "pr_cap", hold_since: ago(40), created_at: ago(5) }), now)).toBe(40);
  expect(isStale(fixture({ created_at: ago(29) }), now)).toBe(false);
  expect(isStale(fixture({ created_at: ago(30) }), now)).toBe(true);
  expect(isStale(fixture({ created_at: ago(90), state: "merged" }), now)).toBe(false);
  expect(isStale(fixture({ created_at: ago(90), kind: "api", state: "open" }), now)).toBe(false);
});

test("kpis count open and stale PRs, recommendations, holds by reason and failed applies once each", () => {
  const rows = [
    fixture({ id: 1, created_at: ago(2) }),
    fixture({ id: 2, created_at: ago(45) }),
    fixture({ id: 3, kind: "api", state: "recommended" }),
    fixture({ id: 4, kind: "api", state: "failed" }),
    fixture({ id: 5, kind: "api", state: "failed", hold: "permission" }),
    fixture({ id: 6, state: "merged" }),
  ];
  const held = [heldResult({ hold: "conflict", hold_since: ago(10) }), heldResult({ hold: "conflict", hold_since: ago(1), repository_id: 8 }), fixture({ id: 5, kind: "api", state: "failed", hold: "permission" })];
  expect(kpis(rows, held, now)).toEqual({
    open: 2,
    stale: 1,
    recommended: 1,
    held: 3,
    heldByReason: [
      { hold: "conflict", count: 2 },
      { hold: "permission", count: 1 },
    ],
    failed: 2,
    outstanding: 5,
  });
});

test("every state and every hold renders a badge: the states by tone, the holds with their sentence", () => {
  for (const state of STATES) {
    const html = renderToStaticMarkup(RemediationStateBadge({ state }));
    expect(html).toContain(state === "open" ? "pr open" : state.replaceAll("_", " "));
  }
  for (const hold of HOLDS) {
    const html = renderToStaticMarkup(HoldBadge({ hold }));
    expect(html).toContain(hold.replaceAll("_", " "));
    expect(html).toContain(`title="${holdSentence(hold)}"`);
  }
  expect(renderToStaticMarkup(HoldBadge({ hold: "teleported" }))).toContain("teleported");
});
