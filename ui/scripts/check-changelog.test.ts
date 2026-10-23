import { describe, expect, test } from "bun:test";
import { readFile } from "node:fs/promises";

import { bundlePath, chartAppVersion, type ChangelogEntry, parseChangelog, renderBundle, toBundle, validateChangelog } from "./changelog";

const entry = (p: Partial<ChangelogEntry>): ChangelogEntry => ({ version: "2.0.0-rc.15", date: "2026-10-23", title: "t", summary: "s", tags: ["fixed", "ui"], ...p });

const good = [entry({}), entry({ version: "2.0.0-rc.14", date: "2026-10-21" }), entry({ version: "2.0.0-rc.13", date: "2026-10-21", tags: ["new"] })];

describe("validateChangelog", () => {
  test("a well-formed changelog passes; equal dates may share a day", () => {
    expect(validateChangelog(good, "2.0.0-rc.15")).toEqual([]);
  });

  test.each<[string, ChangelogEntry[], string, RegExp]>([
    ["the newest entry is not the chart appVersion", good, "2.0.0-rc.16", /newest entry is 2\.0\.0-rc\.15 but .*appVersion is 2\.0\.0-rc\.16/],
    ["a tag outside the closed set", [entry({ tags: ["fixed", "backend"] })], "2.0.0-rc.15", /tag "backend" is not one of/],
    ["an entry with only an area tag", [entry({ tags: ["ui"] })], "2.0.0-rc.15", /needs one of new, improved, fixed, breaking/],
    ["dates that ascend", [entry({}), entry({ version: "2.0.0-rc.14", date: "2026-10-24" })], "2.0.0-rc.15", /date 2026-10-24 is after the newer entry's 2026-10-23/],
    ["a version that repeats", [entry({}), entry({ date: "2026-10-22" })], "2.0.0-rc.15", /version repeats/],
    ["a date that is not a day", [entry({ date: "yesterday" })], "2.0.0-rc.15", /is not YYYY-MM-DD/],
    ["no entries", [], "2.0.0-rc.15", /no entries/],
  ])("fails on %s", (_name, entries, appVersion, want) => {
    const errors = validateChangelog(entries, appVersion);
    expect(errors.length).toBeGreaterThan(0);
    expect(errors.some((e) => want.test(e))).toBe(true);
  });
});

describe("parseChangelog", () => {
  test("reads the YAML list and keeps a date as the day written", () => {
    const entries = parseChangelog(`- version: 2.0.0-rc.15\n  date: 2026-10-23\n  title: T\n  summary: S\n  tags: [fixed, ui]\n`);
    expect(entries).toEqual([{ version: "2.0.0-rc.15", date: "2026-10-23", title: "T", summary: "S", tags: ["fixed", "ui"] }]);
  });

  test.each([
    ["a mapping, not a list", `version: 1\n`, /must be a list/],
    ["a missing summary", `- version: "1"\n  date: 2026-10-23\n  title: T\n  tags: [fixed]\n`, /summary must be a non-empty string/],
    ["empty tags", `- version: "1"\n  date: 2026-10-23\n  title: T\n  summary: S\n  tags: []\n`, /tags must be a non-empty list/],
  ])("fails on %s", (_name, yaml, want) => {
    expect(() => parseChangelog(yaml)).toThrow(want);
  });
});

test("chartAppVersion reads the quoted appVersion line", () => {
  expect(chartAppVersion(`apiVersion: v2\nversion: 2.0.0-rc.15\nappVersion: "2.0.0-rc.15"\n`)).toBe("2.0.0-rc.15");
  expect(() => chartAppVersion(`version: 1\n`)).toThrow(/no appVersion/);
});

// The committed artefacts agree with each other and with the chart, so
// the gate passes on this tree: the same checks check-changelog.ts runs.
test("CHANGELOG.yaml validates against the chart and the committed bundle is current", async () => {
  const [yaml, chart, committed] = await Promise.all([readFile("CHANGELOG.yaml", "utf8"), readFile("../charts/repo-guardian/Chart.yaml", "utf8"), readFile(bundlePath, "utf8")]);
  const entries = parseChangelog(yaml);
  expect(validateChangelog(entries, chartAppVersion(chart))).toEqual([]);
  expect(renderBundle(toBundle(entries))).toBe(committed);
});
