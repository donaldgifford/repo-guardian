import AxeBuilder from "@axe-core/playwright";
import { expect, type Locator, type Page, test } from "@playwright/test";

// IMPL-0031 6.1: axe at wcag2aa over every page: the public status page
// and each controls view inside the signed-in shell (IMPL-0029 8.8).
// The badges carry their tones' contrast.
const TAGS = ["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"];

async function signIn(page: Page, path = "/") {
  await page.goto(path);
  await expect(page.getByRole("button", { name: /^Account menu/ })).toBeVisible();
}

// settled waits for an overlay's open animation: axe reads colours
// mid-fade, and a palette still fading in fails on contrast. Scoped to
// the overlay, since the document's infinite animations never finish.
async function settled(overlay: Locator) {
  await overlay.evaluate((el) => Promise.all(el.getAnimations({ subtree: true }).map((a) => a.finished)));
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
  await page.getByRole("table", { name: "Findings" }).getByRole("row").filter({ hasText: "web" }).filter({ hasText: "renovate" }).click();
  await expect(page.getByRole("dialog", { name: /^Finding: acme\/web renovate/ })).toBeVisible();
  await page.waitForLoadState("networkidle");
  await audit(page);
});

test("a finding drawer deep link has no axe violations", async ({ page }) => {
  await signIn(page, "/findings");
  const repos = (await (await page.request.get("/api/v1/repositories?q=mobile")).json()) as { items: { id: number; name: string }[] };
  await page.goto(`/findings?open=${repos.items.find((r) => r.name === "mobile")!.id}/codeowners`);
  await expect(page.getByRole("dialog", { name: /^Finding: acme\/mobile codeowners/ })).toBeVisible();
  await page.waitForLoadState("networkidle");
  await audit(page);
});

test("a remediation drawer has no axe violations", async ({ page }) => {
  await signIn(page, "/remediations?view=recommended");
  await page.getByRole("table", { name: "Remediations" }).locator("tbody tr").first().click();
  await expect(page.getByRole("dialog", { name: /^Remediation: acme\/mobile labels/ })).toBeVisible();
  await page.waitForLoadState("networkidle");
  await audit(page);
});

test("a remediation drawer deep link has no axe violations", async ({ page }) => {
  await signIn(page, "/remediations");
  const repos = (await (await page.request.get("/api/v1/repositories?q=infra")).json()) as { items: { id: number; name: string }[] };
  await page.goto(`/remediations?open=${repos.items.find((r) => r.name === "infra")!.id}/codeowners`);
  await expect(page.getByRole("dialog", { name: /^Remediation: acme\/infra codeowners/ })).toBeVisible();
  await page.waitForLoadState("networkidle");
  await audit(page);
});

// The frame's other states (IMPL-0037 6.14): the collapsed rail, whose
// entries are named by their tooltips' labels, the open sheet below
// 768px, the account menu and the palette.
test("the collapsed rail has no axe violations", async ({ page }) => {
  await signIn(page, "/");
  await page.locator('[data-slot="sidebar-trigger"]').click();
  await expect(page.locator('[data-slot="sidebar"][data-state]')).toHaveAttribute("data-state", "collapsed");
  await page.waitForLoadState("networkidle");
  await audit(page);
  // A rail entry shows its label as a tooltip when Tab reaches it.
  await page.getByRole("navigation", { name: "Primary" }).getByRole("link", { name: "Fleet" }).focus();
  await page.keyboard.press("Tab");
  await expect(page.getByRole("navigation", { name: "Primary" }).getByRole("link", { name: "Findings" })).toBeFocused();
  await expect(page.getByRole("tooltip")).toContainText("Findings");
  await audit(page);
  // Leave the next test an expanded sidebar.
  await page.locator('[data-slot="sidebar-trigger"]').click();
});

test("the open sheet has no axe violations", async ({ page }) => {
  await page.setViewportSize({ width: 600, height: 900 });
  await page.goto("/");
  await page.locator('[data-slot="sidebar-trigger"]').click();
  await expect(page.getByRole("dialog")).toBeVisible();
  await settled(page.getByRole("dialog"));
  await page.waitForLoadState("networkidle");
  await audit(page);
});

test("the account menu and the palette have no axe violations", async ({ page }) => {
  await signIn(page, "/");
  await page.getByRole("button", { name: /^Account menu/ }).click();
  await expect(page.getByRole("menu")).toBeVisible();
  await settled(page.getByRole("menu"));
  await audit(page);
  await page.keyboard.press("Escape");
  await expect(page.getByRole("menu")).toHaveCount(0);
  await page.keyboard.press("ControlOrMeta+k");
  await page.getByRole("combobox").fill("re");
  await expect(page.getByRole("option").first()).toBeVisible();
  await settled(page.getByRole("dialog"));
  await audit(page);
});
