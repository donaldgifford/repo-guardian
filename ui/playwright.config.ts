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
  use: {
    baseURL: "http://localhost:3100",
    trace: "retain-on-failure",
  },
  projects: [{ name: "chromium", use: { ...devices["Desktop Chrome"] } }],
  webServer: {
    command: "bun e2e/stack.ts",
    url: "http://localhost:3100/healthz",
    timeout: 240_000,
    reuseExistingServer: !process.env.CI,
    stdout: "pipe",
  },
});
