import { expect, type Page, test } from "@playwright/test";

// signIn runs the real code flow: the BFF redirects to the mock
// issuer, which approves at once and sends the browser back.
async function signIn(page: Page, path = "/") {
  await page.goto(path);
  await expect(page.getByRole("button", { name: "Sign out" })).toBeVisible();
}

test("an app path without a session goes through login and comes back", async ({ page }) => {
  await page.goto("/findings?status=non_compliant");
  await expect(page).toHaveURL(/\/findings\?status=non_compliant$/);
  await expect(page.getByRole("heading", { name: "Findings" })).toBeVisible();
  // The API names the caller by preferred_username (OIDC_NAME_CLAIM).
  await expect(page.getByRole("banner").getByText("ada", { exact: true })).toBeVisible();
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

test("Fleet → Rule → Repository navigation", async ({ page }) => {
  await signIn(page);
  await expect(page.getByRole("heading", { name: "Fleet" })).toBeVisible();
  await expect(page.getByText("Tracked")).toBeVisible();

  await page.getByRole("link", { name: /renovate/ }).first().click();
  await expect(page.getByRole("heading", { name: "renovate" })).toBeVisible();
  await expect(page.getByText("Dependencies are kept current by Renovate.")).toBeVisible();

  await page.getByRole("link", { name: "acme/web" }).click();
  await expect(page.getByRole("heading", { name: "acme/web" })).toBeVisible();
  await expect(page.getByText("gate closed: renovate not satisfied")).toBeVisible();
});

test("scoping: a user in acme-devs never sees globex", async ({ page }) => {
  await signIn(page, "/orgs");
  await expect(page.getByRole("link", { name: "acme" })).toBeVisible();
  await expect(page.getByText("globex")).toHaveCount(0);

  await page.goto("/findings");
  await expect(page.getByRole("link", { name: "acme/api" }).first()).toBeVisible();
  await expect(page.getByText("globex/site")).toHaveCount(0);
});

test("filters live in the URL", async ({ page }) => {
  await signIn(page, "/findings");
  await page.getByLabel("status").selectOption("non_compliant");
  await expect(page).toHaveURL(/status=non_compliant/);
  // acme/api has three findings and one failing: the count converges
  // once the filtered page replaces the previous one.
  await expect(page.getByRole("link", { name: "acme/api" })).toHaveCount(1);
  await expect(page.getByRole("cell", { name: "codeowners", exact: true })).toBeVisible();
  await expect(page.getByRole("cell", { name: "dependabot", exact: true })).toHaveCount(0);

  await page.getByLabel("Org").selectOption("acme");
  await expect(page).toHaveURL(/org=acme/);
  await page.getByRole("link", { name: "Rules" }).click();
  await expect(page).toHaveURL(/\/rules\?org=acme/);

  await page.goto("/findings?reason=gate_closed");
  await expect(page.getByLabel("reason")).toHaveValue("gate_closed");
  await expect(page.getByRole("link", { name: "acme/web" })).toHaveCount(1);
});

test("HTML in evidence renders as text, and PR links are built, not copied", async ({ page }) => {
  await signIn(page, "/repos/1");
  await expect(page.getByText('<img src=x onerror="window.__xss=1">extends must include config:base')).toBeVisible();
  expect(await page.evaluate(() => (window as unknown as { __xss?: number }).__xss)).toBeUndefined();
  expect(await page.locator("main img").count()).toBe(0);

  const pr = page.getByRole("link", { name: /PR #412/ });
  await expect(pr).toHaveAttribute("href", "https://github.com/acme/web/pull/412");
  await expect(pr).toHaveAttribute("rel", "noopener noreferrer");
});

test("migrated findings say they were last checked by v1", async ({ page }) => {
  await signIn(page, "/repos/5");
  await expect(page.getByRole("heading", { name: "acme/docs" })).toBeVisible();
  await expect(page.getByText("last checked by v1; details on next check")).toBeVisible();
});

test("file_missing evidence lists every path checked", async ({ page }) => {
  await signIn(page, "/findings?reason=file_missing");
  await expect(page.getByText("checked CODEOWNERS", { exact: true })).toBeVisible();
  await expect(page.getByText("checked .github/CODEOWNERS")).toBeVisible();
});

test("the status page is public", async ({ browser }) => {
  const context = await browser.newContext();
  const page = await context.newPage();
  await page.goto("/status");
  await expect(page).toHaveURL(/\/status$/);
  await expect(page.getByRole("heading", { name: "repo-guardian status" })).toBeVisible();
  await expect(page.getByText("operational").first()).toBeVisible();
  await expect(page.getByText("Fleet compliance:")).toBeVisible();
  expect(await context.cookies()).toHaveLength(0);
  await context.close();
});

test("CSV export downloads the loaded findings", async ({ page }) => {
  await signIn(page, "/findings");
  const [download] = await Promise.all([page.waitForEvent("download"), page.getByRole("button", { name: /Export CSV/ }).click()]);
  expect(download.suggestedFilename()).toBe("findings.csv");
  const text = await (await download.createReadStream()).toArray();
  const csv = Buffer.concat(text).toString("utf8");
  expect(csv.split("\r\n")[0]).toBe("org,repository,rule_kind,rule_name,status,reason,remediation,status_since,pr_number,pr_stale");
  expect(csv).toContain("acme,web,file,renovate,non_compliant,assertion_failed,pr_open");
  expect(csv).not.toContain("globex");
});

test("signing out ends the session", async ({ page, context }) => {
  await signIn(page);
  await page.getByRole("button", { name: "Sign out" }).click();
  await expect.poll(async () => (await context.cookies()).filter((c) => c.name.startsWith("__Host-rg_session"))).toHaveLength(0);
});
