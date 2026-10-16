import { expect, type Page, test } from "@playwright/test";

// IMPL-0031 6.2 (DESIGN-0036 OQ5): one screenshot per reference page at
// 1600×1000. The snapshots under e2e/__screenshots__/ are rendered by
// the Chromium inside the pinned mcr.microsoft.com/playwright image, so
// fonts and anti-aliasing are the same on every machine; the spec only
// runs when the test runner is connected to that browser
// (PW_TEST_CONNECT_WS_ENDPOINT, set by scripts/visual.ts), and is
// skipped with a pointer otherwise. The seed stamps everything relative
// to now(), so the clock is frozen at the real time (relative labels
// stay put) and <time> elements and chart x-axes are masked (absolute
// dates move daily).
const remote = Boolean(process.env.PW_TEST_CONNECT_WS_ENDPOINT);
test.skip(!remote, "screenshots are rendered by the pinned Playwright image: run `make test-ui-e2e-visual`, or `make test-ui-e2e-update-snapshots` to regenerate them");

test.use({ viewport: { width: 1600, height: 1000 }, timezoneId: "UTC", locale: "en-US" });

async function snapshot(page: Page, name: string) {
  await page.clock.setFixedTime(Date.now());
  await page.mouse.move(0, 0);
  await page.waitForLoadState("networkidle");
  await expect(page).toHaveScreenshot(name, { maxDiffPixelRatio: 0.002, mask: [page.locator("time"), page.locator(".recharts-xAxis")] });
}

async function signIn(page: Page, path = "/") {
  await page.goto(path);
  await expect(page.getByRole("button", { name: "Account menu" })).toBeVisible();
}

test("Fleet", async ({ page }) => {
  await signIn(page);
  await expect(page.getByRole("term").filter({ hasText: "Tracked" })).toBeVisible();
  await expect(page.getByRole("img", { name: /over 90d$/ }).first()).toBeVisible();
  await snapshot(page, "fleet.png");
});

test("Orgs", async ({ page }) => {
  await signIn(page, "/orgs?open=acme");
  await expect(page.locator("tr[data-children=acme]").getByRole("link").first()).toBeVisible();
  await snapshot(page, "orgs.png");
});

test("Findings with the drawer open", async ({ page }) => {
  await signIn(page, "/findings");
  await page.goto("/findings?open=1/file/renovate");
  await expect(page.locator('[data-drawer="inline"]')).toBeVisible();
  await expect(page.locator('[role="dialog"] [aria-label="Timeline"] li').first()).toBeVisible();
  await snapshot(page, "findings-drawer.png");
});

test("Repository", async ({ page }) => {
  await signIn(page, "/repos/1");
  await expect(page.getByRole("heading", { name: "acme/web" })).toBeVisible();
  await expect(page.getByRole("heading", { name: "Recent checks" })).toBeVisible();
  await expect(page.locator('[aria-label="Timeline"] li').first()).toBeVisible();
  await snapshot(page, "repository.png");
});

test("Status", async ({ page }) => {
  await page.goto("/status");
  await expect(page.locator("[data-component]").first()).toBeVisible();
  await snapshot(page, "status.png");
});
