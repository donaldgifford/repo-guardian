// The DOM must exist before radix loads: keep this first.
import { withDOM } from "../../testing/dom";

import { describe, expect, test } from "bun:test";
import { render, waitFor } from "@testing-library/react";

import { AppHarness } from "../../testing/app";
import { NOT_ENABLED } from "../components/GatedPanel";
import { OpenPRsPanel, ThroughputPanel } from "./fleet";

withDOM();

const buckets = [
  { bucket: "<1d", count: 1 },
  { bucket: "1-7d", count: 2 },
  { bucket: "7-30d", count: 0 },
  { bucket: "30d+", count: 3 },
];
const none = buckets.map((b) => ({ ...b, count: 0 }));
const activity = [
  { day: "2026-10-22", opened: 2, closed: 1 },
  { day: "2026-10-23", opened: 0, closed: 3 },
];

// The two gated panels decide only from the Remediation App's presence:
// loading until /status answers, the one sentence without it, the
// caller's empty state with it and nothing to show, else the chart.
describe("the gated PR panels", () => {
  const gated = async (container: HTMLElement, want: string) => {
    await waitFor(() => expect(container.querySelector("[data-gated]")?.getAttribute("data-gated")).toBe(want));
  };

  test("Open PRs by age", async () => {
    const at = (enabled: boolean | undefined, b: typeof buckets) =>
      render(
        <AppHarness>
          <OpenPRsPanel enabled={enabled} buckets={b} org="acme" />
        </AppHarness>,
      );
    const loading = at(undefined, buckets);
    await gated(loading.container, "loading");
    loading.unmount();

    const off = at(false, buckets);
    await gated(off.container, "not_enabled");
    expect(off.getByText(NOT_ENABLED)).toBeTruthy();
    expect(off.queryByRole("img")).toBeNull();
    off.unmount();

    const empty = at(true, none);
    await gated(empty.container, "empty");
    expect(empty.getByText("No open pull requests.")).toBeTruthy();
    empty.unmount();

    const on = at(true, buckets);
    await gated(on.container, "content");
    expect(on.getByRole("img", { name: "Open pull requests by age" })).toBeTruthy();
    const link = on.getByRole("link", { name: "findings?pr_stale=true" });
    expect(link.getAttribute("href")).toBe("/findings?pr_stale=true&org=acme");
    expect(on.container.textContent).toContain("3 over threshold");
    on.unmount();
  });

  test("PR throughput", async () => {
    const at = (enabled: boolean | undefined, days: typeof activity | undefined) =>
      render(
        <AppHarness>
          <ThroughputPanel enabled={enabled} days={days} />
        </AppHarness>,
      );
    const states: [boolean | undefined, typeof activity | undefined, string][] = [
      [undefined, undefined, "loading"],
      [false, undefined, "not_enabled"],
      [true, activity.map((d) => ({ ...d, opened: 0, closed: 0 })), "empty"],
      [true, activity, "content"],
    ];
    for (const [enabled, days, want] of states) {
      const r = at(enabled, days);
      await gated(r.container, want);
      if (want === "not_enabled") expect(r.getByText(NOT_ENABLED)).toBeTruthy();
      if (want === "content") expect(r.getByRole("img", { name: "Pull requests opened and closed per day" })).toBeTruthy();
      r.unmount();
    }
  });
});
