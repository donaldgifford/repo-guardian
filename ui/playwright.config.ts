import { defineConfig, devices } from "@playwright/test";

// The e2e suite (IMPL-0025 22.4) runs against e2e/stack.ts: seeded
// Postgres, the mock issuer, the Go api role and the BFF. It needs
// Docker and Go on the PATH.
export default defineConfig({
  testDir: "e2e",
  globalTeardown: "./e2e/teardown.ts",
  fullyParallel: false,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 1 : 0,
  reporter: process.env.CI ? [["list"], ["html", { open: "never" }]] : "list",
  // Screenshots (e2e/visual.spec.ts) have one rendering environment, the
  // pinned Playwright image, so the path carries no platform suffix.
  snapshotPathTemplate: "e2e/__screenshots__/{arg}{ext}",
  expect: { toHaveScreenshot: { maxDiffPixelRatio: 0.002 } },
  use: {
    baseURL: "http://localhost:3100",
    trace: "retain-on-failure",
  },
  // The no-remediation project (IMPL-0032 8.7) runs its one spec against
  // the stack's second api role and BFF, seeded without the Remediation
  // App; every other spec runs against the main one.
  projects: [
    { name: "chromium", use: { ...devices["Desktop Chrome"] }, testIgnore: /no-remediation\.spec\.ts/ },
    { name: "no-remediation", testMatch: /no-remediation\.spec\.ts/, use: { ...devices["Desktop Chrome"], baseURL: "http://localhost:3101" } },
  ],
  webServer: {
    command: "bun e2e/stack.ts",
    url: "http://localhost:3100/healthz",
    timeout: 240_000,
    reuseExistingServer: !process.env.CI,
    stdout: "pipe",
  },
});
