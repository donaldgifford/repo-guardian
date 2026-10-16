import AxeBuilder from "@axe-core/playwright";
import { expect, type Page, test } from "@playwright/test";

// IMPL-0031 6.1: axe at wcag2aa over the five reference pages. Badge
// tone contrast is covered by the run itself: every tone appears on
// Fleet (worst rules), Findings (status, reason, remediation) and
// Status (state badges), so a failing pair fails here, not in review.
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

test("Fleet has no axe violations", async ({ page }) => {
  await signIn(page);
  await expect(page.getByRole("heading", { name: "Fleet" })).toBeVisible();
  await expect(page.getByRole("term").filter({ hasText: "Tracked" })).toBeVisible();
  await audit(page);
});

test("Orgs has no axe violations", async ({ page }) => {
  await signIn(page, "/orgs");
  await expect(page.getByRole("link", { name: "acme" })).toBeVisible();
  await audit(page);
});

test("Findings with the drawer open has no axe violations", async ({ page }) => {
  await signIn(page, "/findings");
  // Signed in first: under the modal drawer the shell is aria-hidden.
  await page.goto("/findings?open=1/file/renovate");
  await expect(page.locator('[role="dialog"][aria-label="Finding"]')).toBeVisible();
  await expect(page.locator('[role="dialog"] [aria-label="Evidence"]')).toBeVisible();
  await audit(page);
});

test("Repository has no axe violations", async ({ page }) => {
  await signIn(page, "/repos/1");
  await expect(page.getByRole("heading", { name: "acme/web" })).toBeVisible();
  await expect(page.getByRole("heading", { name: "Recent checks" })).toBeVisible();
  await audit(page);
});

test("Status has no axe violations", async ({ page }) => {
  await page.goto("/status");
  await expect(page.getByRole("heading", { name: "Repo Guardian status" })).toBeVisible();
  await expect(page.locator("[data-component]").first()).toBeVisible();
  await audit(page);
});
