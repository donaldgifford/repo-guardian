import { expect, type Page, test } from "@playwright/test";

// The "not enabled" stack (IMPL-0032 8.7, DESIGN-0037 OQ6): the same
// seed without the Remediation App, served by the stack's second api
// role and BFF (playwright.config.ts's no-remediation project). Every
// gated panel reads "Remediation is not enabled on this deployment",
// the Remediations nav count is blank and the status backlog cell is
// absent — never an empty state, never a zero.
const NOT_ENABLED = "Remediation is not enabled on this deployment";

async function signIn(page: Page, path = "/") {
  await page.goto(path);
  await expect(page.getByRole("button", { name: "Account menu" })).toBeVisible();
}

async function expectNoGlobex(page: Page) {
  await expect(page.locator("main")).not.toContainText(/globex/i);
}

test("Fleet's PR panels read not enabled", async ({ page }) => {
  await signIn(page);
  await expect(page.getByRole("heading", { level: 1, name: "Fleet" })).toBeVisible();
  for (const name of [/^Open PRs by age/, /^PR throughput/]) {
    const card = page.getByRole("heading", { name }).locator("..").locator("..").locator("..");
    await expect(card.locator("[data-gated]")).toHaveAttribute("data-gated", "not_enabled");
    await expect(card).toContainText(NOT_ENABLED);
  }
  await expect(page.getByRole("navigation", { name: "Primary" }).getByRole("link", { name: /^Remediations/ }).locator("em")).toHaveCount(0);
  await expectNoGlobex(page);
});

test("Findings' remediation column and drawer read not enabled, and pr_age is not offered", async ({ page }) => {
  await signIn(page, "/findings");
  const table = page.getByRole("table", { name: "Findings" });
  await expect(table.locator("tbody tr").first()).toBeVisible();
  await expect(table.locator('[data-gated="not_enabled"]').first()).toHaveAttribute("title", NOT_ENABLED);
  await expect(page.getByRole("tab", { name: /PR age/ })).toHaveCount(0);
  await table.getByRole("row").filter({ hasText: "web" }).filter({ hasText: "renovate" }).click();
  const drawer = page.getByRole("dialog", { name: /^Finding: acme\/web renovate/ });
  await expect(drawer).toContainText(NOT_ENABLED);
  await expectNoGlobex(page);
});

test("the orgs tree's PR activity reads not enabled", async ({ page }) => {
  await signIn(page, "/orgs?tab=pr");
  await expect(page.getByRole("heading", { level: 1, name: "Orgs" })).toBeVisible();
  await expect(page.locator('[data-gated="not_enabled"]').first()).toBeVisible();
  await expect(page.locator("main")).toContainText(NOT_ENABLED);
  await expectNoGlobex(page);
});

test("a repository's control cards read not enabled", async ({ page }) => {
  await signIn(page, "/repositories?org=acme");
  await page.getByRole("table", { name: "Repositories" }).getByRole("link", { name: "acme/web" }).click();
  await expect(page.getByRole("heading", { level: 1, name: "acme/web" })).toBeVisible();
  const cards = page.locator('[data-control] [data-gated="not_enabled"]');
  await expect(cards.first()).toContainText(NOT_ENABLED);
  expect(await cards.count()).toBe(await page.locator("[data-control]").count());
  await expect(page.locator("[data-remediation]")).toHaveCount(0);
  await expectNoGlobex(page);
});

test("the Remediations page is the not-enabled state and its nav count is blank", async ({ page }) => {
  await signIn(page, "/remediations");
  await expect(page.getByRole("heading", { level: 1, name: "Remediations" })).toBeVisible();
  await expect(page.locator('[data-gated="not_enabled"]')).toContainText(NOT_ENABLED);
  await expect(page.getByRole("table", { name: "Remediations" })).toHaveCount(0);
  await expect(page.getByRole("navigation", { name: "Primary" }).getByRole("link", { name: /^Remediations/ }).locator("em")).toHaveCount(0);
  await expectNoGlobex(page);
});

test("the status page has no Remediation App and no backlog cell", async ({ page }) => {
  await page.goto("/status");
  await expect(page.locator('[data-app="eval"]')).toContainText("2 installations");
  await expect(page.locator('[data-app="remediate"]')).toHaveCount(0);
  const ops = page.getByRole("region", { name: "Operations" });
  await expect(ops.getByText("Remediation backlog")).toHaveCount(0);
  await expect(ops.getByText("Overdue evaluations")).toBeVisible();
});
