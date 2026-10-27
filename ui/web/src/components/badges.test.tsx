import { describe, expect, test } from "bun:test";
import { renderToStaticMarkup } from "react-dom/server";

import { KindBadge, ReasonBadge, RemediationBadge, RemediationStateBadge, RuleStatusBadge, StateBadge, StatusBadge } from "./badges";

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
  // IMPL-0032 6.3: the schema's remediation states, read as an open
  // enum. open is in flight, the landed states are ok, a recommendation
  // waits for a human, failed is bad, anything else is over and mute.
  test.each([
    ["open", "info", "pr open"],
    ["merged", "ok", "merged"],
    ["closed_compliant", "ok", "closed compliant"],
    ["resolved", "ok", "resolved"],
    ["applied", "ok", "applied"],
    ["recommended", "warn", "recommended"],
    ["failed", "bad", "failed"],
    ["reserved", "muted-foreground", "reserved"],
    ["closed_by_user", "muted-foreground", "closed by user"],
    ["superseded", "muted-foreground", "superseded"],
    ["withdrawn", "muted-foreground", "withdrawn"],
    ["teleported", "muted-foreground", "teleported"],
  ])("%s → %s, labelled %p", (state, tone, label) => {
    const html = renderToStaticMarkup(<RemediationStateBadge state={state} />);
    expect(toneOf(html)).toBe(tone);
    expect(html).toContain(`>${label}<`);
  });

  const now = new Date("2026-10-23T00:00:00Z");
  const pr = (state: string, daysAgo: number) => ({ kind: "pull_request", state, created_at: new Date(now.getTime() - daysAgo * 86_400_000).toISOString(), pr_number: 7, pr_url: "https://github.com/acme/web/pull/7" });

  test("links the pull request, shows its age and marks an open one stale at 30 days", () => {
    const fresh = renderToStaticMarkup(<RemediationBadge remediation={pr("open", 3)} now={now} />);
    expect(fresh).toContain('href="https://github.com/acme/web/pull/7"');
    expect(fresh).toContain("#7");
    expect(fresh).toContain(">3d<");
    expect(fresh).not.toContain("stale");
    const old = renderToStaticMarkup(<RemediationBadge remediation={pr("open", 30)} now={now} />);
    expect(old).toContain(">stale<");
    // A merged pull request is never stale, however old.
    expect(renderToStaticMarkup(<RemediationBadge remediation={pr("merged", 90)} now={now} />)).not.toContain("stale");
    // An API remediation has no pull request to link.
    const api = renderToStaticMarkup(<RemediationBadge remediation={{ kind: "api", state: "recommended", created_at: pr("x", 1).created_at }} now={now} />);
    expect(api).not.toContain("href=");
    expect(api).toContain(">recommended<");
  });

  test("absent is a dash", () => {
    expect(renderToStaticMarkup(<RemediationBadge remediation={undefined} />)).toBe('<span class="text-muted-foreground">—</span>');
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
