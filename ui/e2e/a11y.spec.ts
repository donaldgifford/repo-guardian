import AxeBuilder from "@axe-core/playwright";
import { expect, type Page, test } from "@playwright/test";

// IMPL-0031 6.1: axe at wcag2aa over every page: the public status page
// and each controls view inside the signed-in shell (IMPL-0029 8.8).
// The badges carry their tones' contrast.
const TAGS = ["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"];

async function signIn(page: Page, path = "/") {
  await page.goto(path);
  await expect(page.getByRole("button", { name: "Account menu" })).toBeVisible();
}

async function audit(page: Page) {
  const results = await new AxeBuilder({ page }).withTags(TAGS).analyze();
  const summary = results.violations.map((v) => ({
    id: v.id,
    impact: v.impact,
    help: v.help,
    nodes: v.nodes.map((n) => n.target.join(" ")),
  }));
  expect(summary, JSON.stringify(summary, null, 2)).toEqual([]);
}

for (const [name, path, heading] of [
  ["Fleet", "/", "Fleet"],
  ["Controls", "/controls", "Controls"],
  ["Findings", "/findings", "Findings"],
  ["Control", "/controls/codeowners", "codeowners"],
  ["Orgs", "/orgs", "Orgs"],
  ["Orgs expanded", "/orgs?open=acme", "Orgs"],
  ["Orgs parked", "/orgs?tab=parked&open=acme", "Orgs"],
  ["Org", "/orgs/acme", "acme"],
  ["Repositories", "/repositories", "Repositories"],
  ["Remediations", "/remediations", "Remediations"],
  ["Remediations held", "/remediations?view=held", "Remediations"],
  ["History", "/history", "History"],
  ["Policy", "/policy", "Policy"],
] as const) {
  test(`${name} has no axe violations`, async ({ page }) => {
    await signIn(page, path);
    await expect(page.getByRole("heading", { level: 1, name: heading })).toBeVisible();
    await page.waitForLoadState("networkidle");
    await audit(page);
  });
}

test("a repository has no axe violations", async ({ page }) => {
  await signIn(page, "/repositories?org=acme");
  await page.getByRole("table", { name: "Repositories" }).getByRole("link", { name: "acme/web" }).click();
  await expect(page.locator('[data-control="renovate"]')).toBeVisible();
  await audit(page);
});

test("Status has no axe violations", async ({ page }) => {
  await page.goto("/status");
  await expect(page.getByRole("heading", { name: "Repo Guardian status" })).toBeVisible();
  await expect(page.locator("[data-component]").first()).toBeVisible();
  await audit(page);
});

// The drawer is a modal Dialog that aria-hides the page, so the
// sign-in's role query runs first and the ?open= URL is a second goto.
test("a finding drawer has no axe violations", async ({ page }) => {
  await signIn(page, "/findings");
  await page.getByRole("table", { name: "Findings" }).getByRole("row").filter({ hasText: "renovate" }).click();
  await expect(page.getByRole("dialog", { name: /^Finding: acme\/web renovate/ })).toBeVisible();
  await page.waitForLoadState("networkidle");
  await audit(page);
});
