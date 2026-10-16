import AxeBuilder from "@axe-core/playwright";
import { expect, type Page, test } from "@playwright/test";

// IMPL-0031 6.1: axe at wcag2aa over the pages that remain after
// IMPL-0029 7.7: the public status page and the same view inside the
// signed-in shell. The state badges carry their tones' contrast.
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

test("the signed-in overview has no axe violations", async ({ page }) => {
  await signIn(page);
  await expect(page.getByRole("heading", { name: "Repo Guardian status" })).toBeVisible();
  await expect(page.locator("[data-component]").first()).toBeVisible();
  await audit(page);
});

test("Status has no axe violations", async ({ page }) => {
  await page.goto("/status");
  await expect(page.getByRole("heading", { name: "Repo Guardian status" })).toBeVisible();
  await expect(page.locator("[data-component]").first()).toBeVisible();
  await audit(page);
});
