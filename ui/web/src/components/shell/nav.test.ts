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
  expect(NAV.map((n) => n.to)).toEqual(["/", "/status"]);
  expect(NAV[0]?.group).toBe("BUSINESS");
});
