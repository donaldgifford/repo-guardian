import { expect, type Page, test } from "@playwright/test";

// IMPL-0031 6.2 (DESIGN-0036 OQ5): one screenshot per reference page at
// 1600×1000: the status page, and since IMPL-0032 8.4 the Findings,
// Orgs (expanded) and Remediations pages signed in through the mock
// issuer. The snapshots under e2e/__screenshots__/ are rendered by
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

test("Status", async ({ page }) => {
  await page.goto("/status");
  await expect(page.locator("[data-component]").first()).toBeVisible();
  await snapshot(page, "status.png");
});

async function signIn(page: Page, path: string) {
  await page.goto(path);
  await expect(page.getByRole("button", { name: "Account menu" })).toBeVisible();
}

test("Findings", async ({ page }) => {
  await signIn(page, "/findings");
  await expect(page.getByRole("table", { name: "Findings" }).locator("tbody tr").first()).toBeVisible();
  await snapshot(page, "findings.png");
});

test("Orgs", async ({ page }) => {
  await signIn(page, "/orgs?open=acme");
  await expect(page.getByRole("table", { name: "Orgs" }).locator("tr[data-child='acme/web']")).toBeVisible();
  await snapshot(page, "orgs.png");
});

test("Remediations", async ({ page }) => {
  await signIn(page, "/remediations");
  await expect(page.getByRole("table", { name: "Remediations" }).locator("tbody tr").first()).toBeVisible();
  await snapshot(page, "remediations.png");
});
