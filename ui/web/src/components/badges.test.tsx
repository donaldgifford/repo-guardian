import { describe, expect, test } from "bun:test";
import { renderToStaticMarkup } from "react-dom/server";

import { KindBadge, ReasonBadge, RemediationBadge, RuleStatusBadge, StateBadge, StatusBadge } from "./badges";

// A tone is visible in the markup as the --tone variable it sets, or as
// the border for outline.
function toneOf(html: string): string {
  const m = /\[--tone:var\(--color-([a-z-]+)\)\]/.exec(html);
  if (m?.[1]) return m[1];
  return html.includes("border-border") ? "outline" : "?";
}

describe("StatusBadge", () => {
  test.each([
    ["compliant", "ok", "compliant"],
    ["non_compliant", "bad", "non compliant"],
    ["not_applicable", "muted-foreground", "n/a"],
    ["unknown", "warn", "unknown"],
    ["quarantined", "muted-foreground", "quarantined"],
  ])("%s → %s, labelled %p", (status, tone, label) => {
    const html = renderToStaticMarkup(<StatusBadge status={status} />);
    expect(toneOf(html)).toBe(tone);
    expect(html).toContain(`>${label}<`);
  });
});

describe("RuleStatusBadge", () => {
  // INV-0023 F1: a failing rule must read bad, not the mute that the
  // control status row gave it.
  test.each([
    ["pass", "ok", "pass"],
    ["fail", "bad", "fail"],
    ["error", "bad", "error"],
    ["unknown", "warn", "unknown"],
    ["not_applicable", "muted-foreground", "n/a"],
  ])("%s → %s, labelled %p", (status, tone, label) => {
    const html = renderToStaticMarkup(<RuleStatusBadge status={status} />);
    expect(toneOf(html)).toBe(tone);
    expect(html).toContain(`>${label}<`);
  });
});

describe("RemediationBadge", () => {
  test.each([
    ["pr_open", "info"],
    ["applied", "ok"],
    ["foreign_pr", "warn"],
    ["disabled", "muted-foreground"],
    ["dry_run", "muted-foreground"],
    ["none", "muted-foreground"],
    ["teleported", "muted-foreground"],
  ])("%s → %s", (remediation, tone) => {
    expect(toneOf(renderToStaticMarkup(<RemediationBadge remediation={remediation} />))).toBe(tone);
  });

  test("none is a dash in a table and a badge in detail", () => {
    const table = renderToStaticMarkup(<RemediationBadge remediation="none" table />);
    expect(table).toBe('<span class="text-muted-foreground">—</span>');
    expect(renderToStaticMarkup(<RemediationBadge remediation="none" />)).toContain(">none<");
    expect(renderToStaticMarkup(<RemediationBadge remediation="pr_open" table />)).toContain(">pr open<");
  });
});

describe("ReasonBadge", () => {
  test.each([
    ["non_compliant", "warn"],
    ["compliant", "muted-foreground"],
    ["unknown", "muted-foreground"],
    ["quarantined", "muted-foreground"],
  ])("under %s → %s", (status, tone) => {
    const html = renderToStaticMarkup(<ReasonBadge reason="file_missing" status={status} />);
    expect(toneOf(html)).toBe(tone);
    expect(html).toContain(">file_missing<");
  });
});

describe("KindBadge", () => {
  test.each(["file", "setting", "branch_protection", "webhook"])("%s is outline", (kind) => {
    const html = renderToStaticMarkup(<KindBadge kind={kind} />);
    expect(toneOf(html)).toBe("outline");
    expect(html).toContain(`>${kind}<`);
  });
});

describe("StateBadge", () => {
  test.each([
    ["operational", "ok"],
    ["degraded", "warn"],
    ["down", "bad"],
    ["unknown", "bad"],
    ["on_fire", "bad"],
  ])("%s → %s", (state, tone) => {
    expect(toneOf(renderToStaticMarkup(<StateBadge state={state} />))).toBe(tone);
  });
});
