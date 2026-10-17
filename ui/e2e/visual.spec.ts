import { expect, type Page, test } from "@playwright/test";

// IMPL-0031 6.2 (DESIGN-0036 OQ5): one screenshot per reference page at
// 1600×1000; since IMPL-0029 7.7 that is the status page alone. The snapshots under e2e/__screenshots__/ are rendered by
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
