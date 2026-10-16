import { expect, test } from "bun:test";

import type { Schemas } from "../../api/client";
import { NAV, navGroups, navItems } from "./nav";

test("the Docs group exists only with a docs_url", () => {
  expect(navGroups(navItems(undefined)).map(([g]) => g)).toEqual(["BUSINESS", "PUBLIC"]);
  expect(navGroups(navItems("")).map(([g]) => g)).toEqual(["BUSINESS", "PUBLIC"]);
  const withDocs = navGroups(navItems("https://docs.example.com/"));
  expect(withDocs.map(([g]) => g)).toEqual(["BUSINESS", "PUBLIC", "DOCS"]);
  expect(withDocs[2]?.[1]).toEqual([{ group: "DOCS", label: "Documentation", href: "https://docs.example.com/", icon: "book" }]);
});

test("Findings carries the failing count from /summary", () => {
  const findings = NAV.find((n) => n.label === "Findings");
  const summary = { findings: { compliant: 1, non_compliant: 37, not_applicable: 0, unknown: 0 } } as Schemas["Summary"];
  expect(findings?.count?.(summary)).toBe(37);
  expect(NAV.filter((n) => n.count).map((n) => n.label)).toEqual(["Findings"]);
});

test("every in-app entry has a route and the business group leads", () => {
  expect(NAV.map((n) => n.to)).toEqual(["/", "/rules", "/orgs", "/findings", "/status"]);
  expect(NAV[0]?.group).toBe("BUSINESS");
});
