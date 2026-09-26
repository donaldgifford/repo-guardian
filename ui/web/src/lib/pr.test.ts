import { describe, expect, test } from "bun:test";

import { prLabel, prLink, repoLink } from "./pr";

describe("prLink", () => {
  test("builds the URL from structured fields", () => {
    expect(prLink("github.com", "acme", "widgets", 412)).toBe("https://github.com/acme/widgets/pull/412");
    expect(prLink("ghe.example.com:8443", "acme", "web.site", 1)).toBe("https://ghe.example.com:8443/acme/web.site/pull/1");
  });

  test.each([
    ["evil.example/x", "acme", "web", 1],
    ["github.com", "acme/../evil", "web", 1],
    ["github.com", "acme", "web?x=1", 1],
    ["github.com", "acme", "web", 0],
    ["github.com", "acme", "web", 1.5],
    ["github.com", "", "web", 1],
  ])("refuses %p %p %p %p", (host, org, repo, n) => {
    expect(prLink(host, org, repo, n)).toBeUndefined();
  });

  test("repoLink follows the same rules", () => {
    expect(repoLink("github.com", "acme", "widgets")).toBe("https://github.com/acme/widgets");
    expect(repoLink("github.com", "acme", "javascript:alert(1)")).toBeUndefined();
  });
});

describe("prLabel", () => {
  const now = new Date("2026-09-25T00:00:00Z");

  test("repo-guardian's PR shows its age", () => {
    expect(prLabel({ remediation: "pr_open", pr: { number: 412, url: "", created_at: "2026-09-16T00:00:00Z" } }, now)).toBe("PR #412 (9d)");
  });

  test("a human PR the rule yields to says so", () => {
    expect(prLabel({ remediation: "foreign_pr", pr: { number: 88, url: "" } }, now)).toBe("PR #88 (human)");
  });

  test("no PR, no label", () => {
    expect(prLabel({ remediation: "none" }, now)).toBeUndefined();
  });
});
