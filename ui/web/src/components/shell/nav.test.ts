import { expect, test } from "bun:test";

import { NAV, navGroups, navItems } from "./nav";

test("the Docs group exists only with a docs_url", () => {
  expect(navGroups(navItems(undefined)).map(([g]) => g)).toEqual(["BUSINESS", "PUBLIC"]);
  expect(navGroups(navItems("")).map(([g]) => g)).toEqual(["BUSINESS", "PUBLIC"]);
  const withDocs = navGroups(navItems("https://docs.example.com/"));
  expect(withDocs.map(([g]) => g)).toEqual(["BUSINESS", "PUBLIC", "DOCS"]);
  expect(withDocs[2]?.[1]).toEqual([{ group: "DOCS", label: "Documentation", href: "https://docs.example.com/", icon: "book" }]);
});

test("every in-app entry has a route and the business group leads", () => {
  expect(NAV.map((n) => n.to)).toEqual(["/", "/findings", "/controls", "/orgs", "/repositories", "/remediations", "/history", "/policy", "/status"]);
  expect(NAV[0]?.group).toBe("BUSINESS");
});

test("Findings and Remediations carry their badges; Remediations stays hidden until its route lands", () => {
  expect(NAV.filter((n) => n.badge).map((n) => [n.to, n.badge, n.hidden ?? false])).toEqual([
    ["/findings", "findings", false],
    ["/remediations", "remediations", true],
  ]);
  expect(navItems(undefined).map((n) => n.to)).toEqual(["/", "/findings", "/controls", "/orgs", "/repositories", "/history", "/policy", "/status"]);
});
