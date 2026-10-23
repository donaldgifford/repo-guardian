import { describe, expect, test } from "bun:test";
import { QueryClientProvider } from "@tanstack/react-query";
import { renderToStaticMarkup } from "react-dom/server";

import type { Schemas } from "../api/client";
import { remediationEnabled } from "../api/queries";
import { seededClient } from "../../testing/app";
import { GatedPanel, gatedContent, NOT_ENABLED } from "./GatedPanel";

const status = (apps: Schemas["StatusApp"]["app"][]): Schemas["Status"] =>
  ({ state: "operational", updated_at: "", topology: "all", components: [], apps: apps.map((app) => ({ app, installations: 1, suspended: 0, repositories: 1 })), compliance: { percent: null, coverage: null, measured: false, history: [] }, operations: { evaluated_24h: 0, overdue: 0, tracked: 0, remediation_backlog: 0, rollout_state: "none" } }) as Schemas["Status"];

describe("remediationEnabled", () => {
  test("is true only when Status.apps lists the Remediation App", () => {
    expect(remediationEnabled(undefined)).toBeUndefined();
    expect(remediationEnabled(status([]))).toBe(false);
    expect(remediationEnabled(status(["eval"]))).toBe(false);
    expect(remediationEnabled(status(["eval", "remediate"]))).toBe(true);
  });
});

describe("gatedContent", () => {
  test.each([
    [undefined, true, "loading"],
    [undefined, false, "loading"],
    [false, true, "not_enabled"],
    [false, false, "not_enabled"],
    [true, true, "empty"],
    [true, false, "content"],
  ] as const)("enabled=%p empty=%p → %s", (enabled, isEmpty, want) => {
    expect(gatedContent(enabled, isEmpty)).toBe(want);
  });
});

function mount(s: Schemas["Status"] | undefined, isEmpty: boolean) {
  const client = seededClient();
  if (s) client.setQueryData(["status"], s);
  return renderToStaticMarkup(
    <QueryClientProvider client={client}>
      <GatedPanel isEmpty={isEmpty} empty="No open pull requests">
        <table aria-label="rows" />
      </GatedPanel>
    </QueryClientProvider>,
  );
}

describe("GatedPanel", () => {
  test("without the App it reads not enabled, whatever the data", () => {
    expect(mount(status(["eval"]), false)).toContain(NOT_ENABLED);
    expect(mount(status(["eval"]), true)).toContain(NOT_ENABLED);
  });

  test("with the App it shows the caller's empty state or its rows, never not enabled", () => {
    const empty = mount(status(["eval", "remediate"]), true);
    expect(empty).toContain("No open pull requests");
    expect(empty).not.toContain(NOT_ENABLED);
    const rows = mount(status(["eval", "remediate"]), false);
    expect(rows).toContain('aria-label="rows"');
    expect(rows).not.toContain("No open pull requests");
  });

  test("before /status answers it is loading", () => {
    expect(mount(undefined, false)).toContain("Loading");
  });
});
