// The view imports radix primitives, which decide at module load
// whether a document exists; register the DOM first so this file does
// not poison the portal detection of every later file in the run.
import { withDOM } from "../../testing/dom";

import { expect, test } from "bun:test";

import type { Schemas } from "../api/client";
import { gridCsv, joinOpen, openList, orgRow, type OrgsSearch, parseOrgsSearch, ruleRow } from "./orgs";

withDOM();

const acme: Schemas["OrgSummary"] = {
  org: "acme",
  tracked: 10,
  parked: 1,
  counts: { compliant: 7, non_compliant: 2, not_applicable: 1, unknown: 0 },
  compliant_percent: 77.7,
};

test.each<[Record<string, unknown>, OrgsSearch]>([
  [{}, {}],
  [{ heat: "parked", parked: "hide", open: "acme,globex" }, { heat: "parked", parked: "hide", open: "acme,globex" }],
  [{ heat: "bogus", parked: "yes", open: "" }, {}],
])("parseOrgsSearch(%j)", (input, want) => {
  expect(parseOrgsSearch(input)).toEqual(want);
});

test("openList and joinOpen round-trip and drop the key when nothing is open", () => {
  expect(openList(undefined)).toEqual([]);
  expect(openList("acme,,globex")).toEqual(["acme", "globex"]);
  expect(joinOpen([])).toBeUndefined();
  expect(joinOpen(["acme", "globex"])).toBe("acme,globex");
});

test("orgRow and ruleRow fill the shared columns", () => {
  const row = orgRow(acme);
  expect(row.cells).toEqual({ tracked: 10, compliant: 7, failing: 2, parked: 1 });
  expect(row.total).toBe(10);
  expect(row.sub).toBe("77.7% compliant · 1 parked");

  const rule = ruleRow("acme", { kind: "file", name: "renovate", counts: { compliant: 3, non_compliant: 2, not_applicable: 4, unknown: 1 }, compliant_percent: 60 });
  expect(rule.key).toBe("acme/file/renovate");
  expect(rule.cells).toEqual({ tracked: 10, compliant: 3, failing: 2, parked: 0 });
});

test("gridCsv writes the visible columns, every org and a totals row", () => {
  const columns = [
    { key: "tracked", header: "Tracked", tone: "mute" as const },
    { key: "failing", header: "Failing", tone: "bad" as const },
  ];
  const rows = [orgRow(acme), orgRow({ ...acme, org: "=globex", tracked: 4, counts: { ...acme.counts, non_compliant: 3 } })];
  expect(gridCsv(rows, columns, ["acme", "=globex"])).toBe("org,tracked,failing\r\nacme,10,2\r\n'=globex,4,3\r\ntotal,14,5\r\n");
});
