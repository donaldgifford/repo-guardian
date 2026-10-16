import { expect, type Page, test } from "@playwright/test";

// The findings-era views went in IMPL-0029 7.7; until the controls
// views land the suite covers sign-in, the session and the status page.

// signIn runs the real code flow: the BFF redirects to the mock
// issuer, which approves at once and sends the browser back.
async function signIn(page: Page, path = "/") {
  await page.goto(path);
  await expect(page.getByRole("button", { name: "Account menu" })).toBeVisible();
}

test("an app path without a session goes through login and comes back", async ({ page }) => {
  await page.goto("/");
  await expect(page).toHaveURL(/\/$/);
  await expect(page.getByRole("heading", { level: 1, name: "Repo Guardian status" })).toBeVisible();
  await expect(page.getByRole("navigation", { name: "Breadcrumb" })).toBeVisible();
  await expect(page.getByRole("alert")).toHaveCount(0);
  // The API names the caller by preferred_username (OIDC_NAME_CLAIM);
  // the name lives in the avatar menu.
  await page.getByRole("button", { name: "Account menu" }).click();
  await expect(page.getByRole("menu").getByText("ada", { exact: true })).toBeVisible();
  await page.keyboard.press("Escape");
});

test("the session and its tokens never reach page script", async ({ page, context }) => {
  await signIn(page);
  expect(await page.evaluate(() => document.cookie)).toBe("");
  const cookies = await context.cookies();
  const session = cookies.filter((c) => c.name.startsWith("__Host-rg_session"));
  expect(session.length).toBeGreaterThan(0);
  for (const c of session) {
    expect(c.httpOnly).toBe(true);
    expect(c.secure).toBe(true);
    expect(c.sameSite).toBe("Lax");
  }
  const storage = await page.evaluate(() => JSON.stringify({ ...localStorage }) + JSON.stringify({ ...sessionStorage }));
  expect(storage).toBe("{}{}");
});

test("the status page is public", async ({ browser }) => {
  const context = await browser.newContext();
  const page = await context.newPage();
  await page.goto("/status");
  await expect(page).toHaveURL(/\/status$/);
  await expect(page.getByRole("heading", { level: 1, name: "Repo Guardian status" })).toBeVisible();

  // The banner headline follows the API's state through the fixed map.
  const status = (await (await page.request.get("/api/v1/status")).json()) as { state: string; components: { name: string }[] };
  const headlines: Record<string, string> = { operational: "All systems operational", degraded: "Degraded performance", unknown: "Status not yet available" };
  await expect(page.getByRole("heading", { level: 2, name: headlines[status.state] ?? "Service disruption" })).toBeVisible();
  await expect(page.getByRole("heading", { name: "Components" })).toBeVisible();
  await expect(page.getByRole("heading", { name: "Fleet compliance" })).toBeVisible();
  await expect(page.getByRole("meter")).toBeVisible();
  for (const c of status.components) {
    await expect(page.locator(`[data-component="${c.name}"]`)).toBeVisible();
  }

  // Aggregate-only: none of the seed's org or repository names appear.
  // The fixed footer path is removed first; "api" is a seeded repository
  // name and also a path segment of GET /api/v1/status.
  const text = (await page.locator("main").innerText()).replace("GET /api/v1/status", "");
  for (const name of ["acme", "globex", "web", "api", "legacy", "site"]) {
    expect(text).not.toMatch(new RegExp(`\\b${name}\\b`, "i"));
  }
  expect(await context.cookies()).toHaveLength(0);
  await context.close();
});

test("signing out ends the session", async ({ page, context }) => {
  await signIn(page);
  await page.getByRole("button", { name: "Account menu" }).click();
  await page.getByRole("menuitem", { name: "Sign out" }).click();
  await expect.poll(async () => (await context.cookies()).filter((c) => c.name.startsWith("__Host-rg_session"))).toHaveLength(0);
});

test("without UI_DOCS_URL the sidebar has no Docs group", async ({ page }) => {
  await signIn(page);
  const nav = page.getByRole("navigation", { name: "Primary" });
  await expect(nav.getByRole("link", { name: /Overview/ })).toBeVisible();
  await expect(nav.getByText("Docs", { exact: true })).toHaveCount(0);
  await expect(nav.getByRole("link", { name: "Documentation" })).toHaveCount(0);
});
