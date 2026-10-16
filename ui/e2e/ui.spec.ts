import { expect, type Page, test } from "@playwright/test";

// signIn runs the real code flow: the BFF redirects to the mock
// issuer, which approves at once and sends the browser back.
async function signIn(page: Page, path = "/") {
  await page.goto(path);
  await expect(page.getByRole("button", { name: "Account menu" })).toBeVisible();
}

// scopeTo picks an org (or "All orgs") in the page header's scope popover.
async function scopeTo(page: Page, name: string | RegExp) {
  await page.getByRole("button", { name: "Org scope" }).click();
  await page.getByRole("dialog", { name: "Org scope" }).getByRole("button", { name }).click();
}

test("an app path without a session goes through login and comes back", async ({ page }) => {
  await page.goto("/findings?status=non_compliant");
  await expect(page).toHaveURL(/\/findings\?status=non_compliant$/);
  await expect(page.getByRole("heading", { name: "Findings" })).toBeVisible();
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

test("Fleet → Rule → Repository navigation", async ({ page }) => {
  await signIn(page);
  await expect(page.getByRole("heading", { name: "Fleet" })).toBeVisible();
  await expect(page.getByRole("term").filter({ hasText: "Tracked" })).toBeVisible();

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
  await page.getByRole("button", { name: "Add filter" }).click();
  await page.getByRole("dialog", { name: "Add filter" }).getByRole("button", { name: "status" }).click();
  await page.getByRole("button", { name: "non_compliant" }).click();
  await expect(page).toHaveURL(/status=non_compliant/);
  await expect(page.getByRole("button", { name: "status filter: non_compliant" })).toBeVisible();
  // acme/api has three findings and one failing: the count converges
  // once the filtered page replaces the previous one.
  await expect(page.getByRole("link", { name: "acme/api" })).toHaveCount(1);
  await expect(page.getByRole("cell", { name: /^codeowners/ })).toBeVisible();
  await expect(page.getByRole("cell", { name: /^dependabot/ })).toHaveCount(0);

  await scopeTo(page, /acme/);
  await expect(page).toHaveURL(/org=acme/);
  await page.getByRole("navigation", { name: "Primary" }).getByRole("link", { name: "Rules" }).click();
  await expect(page).toHaveURL(/\/rules\?org=acme/);

  // A pasted deep link renders as chips: the URL stays the state.
  await page.goto("/findings?reason=gate_closed");
  await expect(page.getByRole("button", { name: "reason filter: gate_closed" })).toBeVisible();
  await expect(page.getByRole("link", { name: "acme/web" })).toHaveCount(1);
  await page.getByRole("button", { name: "reason filter: gate_closed" }).click();
  await page.getByRole("button", { name: "Clear filter" }).click();
  await expect(page).not.toHaveURL(/reason=/);
});

test("HTML in evidence renders as text in the drawer inset, and PR links are built, not copied", async ({ page }) => {
  // A pasted ?open= opens the drawer straight from the URL; the modal
  // then hides the top bar from the accessibility tree, so wait on the
  // dialog rather than on signIn's account-menu check.
  await page.goto("/findings?open=1/file/renovate");
  const drawer = page.getByRole("dialog", { name: "Finding" });
  const inset = drawer.getByLabel("Evidence");
  await expect(inset.getByText('<img src=x onerror="window.__xss=1">extends must include config:base')).toBeVisible();
  expect(await page.evaluate(() => (window as unknown as { __xss?: number }).__xss)).toBeUndefined();
  expect(await page.locator("img").count()).toBe(0);

  const pr = drawer.getByRole("link", { name: "#412" });
  await expect(pr).toHaveAttribute("href", "https://github.com/acme/web/pull/412");
  await expect(pr).toHaveAttribute("rel", "noopener noreferrer");
  await expect(drawer.getByRole("list", { name: "Timeline" }).getByText(/PR #412 still open \(9d\)/)).toBeVisible();
  // Under the modal the breadcrumb is aria-hidden; read it by CSS.
  await expect(page.locator("nav[aria-label=Breadcrumb]")).toContainText("acme/web");
});

test("the drawer opens from a row, closes on Escape back to the row, and overlays with a scrim below 1600px", async ({ page }) => {
  await page.setViewportSize({ width: 1400, height: 900 });
  await signIn(page, "/findings?status=non_compliant");
  const row = page.getByRole("row").filter({ hasText: "renovate" }).first();
  await row.click();
  await expect(page).toHaveURL(/open=1%2Ffile%2Frenovate|open=1\/file\/renovate/);
  const drawer = page.getByRole("dialog", { name: "Finding" });
  await expect(drawer).toBeVisible();
  await expect(drawer).toHaveAttribute("data-drawer", "overlay");
  await expect(page.locator("main [data-layout]")).toHaveAttribute("data-layout", "stacked");
  // The modal hides the page from the accessibility tree; the column is
  // still rendered (not compact) underneath the scrim.
  await expect(page.locator("main th", { hasText: "Age" })).toBeVisible();

  await page.keyboard.press("Escape");
  await expect(drawer).toHaveCount(0);
  await expect(page).not.toHaveURL(/open=/);
  await expect(row).toBeFocused();
});

test("from 1600px the drawer sits beside the table and the Age column hides", async ({ page }) => {
  await page.setViewportSize({ width: 1700, height: 900 });
  await signIn(page, "/findings?open=2/file/codeowners");
  const drawer = page.getByRole("dialog", { name: "Finding" });
  await expect(drawer).toBeVisible();
  await expect(drawer).toHaveAttribute("data-drawer", "inline");
  await expect(page.locator("main [data-layout]")).toHaveAttribute("data-layout", "side-by-side");
  await expect(page.getByRole("columnheader", { name: "Age" })).toHaveCount(0);
  await expect(page.getByRole("row", { selected: true })).toContainText("codeowners");
  await drawer.getByRole("button", { name: "Close" }).click();
  await expect(drawer).toHaveCount(0);
  await expect(page.getByRole("columnheader", { name: "Age" })).toBeVisible();
});

test("migrated findings say they were last checked by v1", async ({ page }) => {
  await signIn(page, "/repos/5");
  await expect(page.getByRole("heading", { name: "acme/docs" })).toBeVisible();
  await expect(page.getByText("last checked by v1; details on next check")).toBeVisible();
});

test("file_missing evidence lists every path checked", async ({ page }) => {
  await signIn(page, "/repos/2");
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
  await page.getByRole("button", { name: "Account menu" }).click();
  await page.getByRole("menuitem", { name: "Sign out" }).click();
  await expect.poll(async () => (await context.cookies()).filter((c) => c.name.startsWith("__Host-rg_session"))).toHaveLength(0);
});

test("every view renders against the seeded API without a problem", async ({ page }) => {
  await signIn(page);
  const views: [string, string][] = [
    ["/", "Fleet"],
    ["/rules", "Rules"],
    ["/rules/file/codeowners", "codeowners"],
    ["/orgs", "Orgs"],
    ["/orgs/acme", "acme"],
    ["/findings", "Findings"],
    ["/repos/2", "acme/api"],
    ["/history", "History"],
    ["/policy", "Policy"],
  ];
  for (const [path, heading] of views) {
    await page.goto(path);
    await expect(page.getByRole("heading", { level: 1, name: heading })).toBeVisible();
    await expect(page.getByRole("navigation", { name: "Breadcrumb" })).toBeVisible();
    await expect(page.getByRole("alert")).toHaveCount(0);
  }
  // History and Policy show seeded data, not just a heading.
  await page.goto("/policy");
  await expect(page.getByText("Every repository names its owners.")).toBeVisible();
  await page.goto("/history");
  await expect(page.getByRole("heading", { level: 2, name: "acme" })).toBeVisible();
});

// The fleet trend's history query was keyed on a timestamp recomputed
// every render, so the home page refetched it in a loop (#207). Each
// view settles on one fetch (a page or two of history at most).
test("the fleet views fetch compliance history once", async ({ page }) => {
  await signIn(page);
  for (const path of ["/", "/?org=acme"]) {
    let fetches = 0;
    const count = (req: { url(): string }) => {
      if (new URL(req.url()).pathname === "/api/v1/compliance/history") {
        fetches++;
      }
    };
    page.on("request", count);
    await page.goto(path);
    await expect(page.getByText("Compliance (90d)")).toBeVisible();
    await page.waitForTimeout(3000);
    page.off("request", count);
    expect(fetches, path).toBeGreaterThan(0);
    expect(fetches, path).toBeLessThanOrEqual(2);
  }
});

test("a repository outside the caller's orgs is a problem page, not data", async ({ page }) => {
  await signIn(page, "/repos/4");
  await expect(page.getByRole("alert")).toContainText("404");
  await expect(page.getByText("globex")).toHaveCount(0);
});

test("the org scope popover writes ?org= and the scope label follows", async ({ page }) => {
  await signIn(page, "/orgs");
  const scope = page.getByRole("button", { name: "Org scope" });
  await expect(scope).toContainText("All orgs (1)");
  await scopeTo(page, /acme/);
  await expect(page).toHaveURL(/\/orgs\?org=acme$/);
  await expect(scope).toContainText("acme");
  // The scope survives navigation and never offers an org outside the caller's.
  await page.getByRole("navigation", { name: "Primary" }).getByRole("link", { name: "Fleet" }).click();
  await expect(page).toHaveURL(/\/\?org=acme$/);
  await scope.click();
  const dialog = page.getByRole("dialog", { name: "Org scope" });
  await expect(dialog.getByText("globex")).toHaveCount(0);
  await dialog.getByRole("button", { name: /All orgs/ }).click();
  await expect(page).toHaveURL(/\/$/);
});

test("⌘K finds the seeded repository and navigates to it", async ({ page }) => {
  await signIn(page);
  await page.keyboard.press("ControlOrMeta+k");
  const palette = page.getByRole("dialog", { name: "Search" });
  await expect(palette).toBeVisible();
  await palette.getByRole("combobox").fill("web");
  await palette.getByRole("option", { name: /acme\/web/ }).click();
  await expect(page).toHaveURL(/\/repos\/1$/);
  await expect(page.getByRole("heading", { level: 1, name: "acme/web" })).toBeVisible();
});

test("the sidebar Findings count equals /summary", async ({ page }) => {
  await signIn(page);
  const summary = (await (await page.request.get("/api/v1/summary")).json()) as { findings: { non_compliant: number } };
  const findings = page.getByRole("navigation", { name: "Primary" }).getByRole("link", { name: /Findings/ });
  await expect(findings).toContainText(summary.findings.non_compliant.toLocaleString());
});

test("range tabs change the history query's from, and the series pills the chart", async ({ page }) => {
  await signIn(page);
  const froms: string[] = [];
  page.on("request", (req) => {
    const u = new URL(req.url());
    if (u.pathname === "/api/v1/compliance/history") froms.push(u.searchParams.get("from") ?? "");
  });
  await expect(page.getByRole("img", { name: "compliant % over 90d" })).toBeVisible();
  await page.getByRole("tab", { name: "7d" }).click();
  await expect(page).toHaveURL(/range=7d/);
  await expect(page.getByText("Compliance (7d)")).toBeVisible();
  await expect.poll(() => froms.length).toBeGreaterThan(0);
  const last = froms[froms.length - 1] ?? "";
  const days = (Date.now() - Date.parse(last)) / 86_400_000;
  expect(days).toBeGreaterThanOrEqual(7);
  expect(days).toBeLessThan(8);

  await page.getByRole("radio", { name: /failing findings/ }).click();
  await expect(page).toHaveURL(/series=failing/);
  await expect(page.getByRole("img", { name: "failing findings over 7d" })).toBeVisible();
  await expect(page.getByRole("radio", { name: /failing findings/ })).toHaveAttribute("aria-checked", "true");
});

test("the fleet issues at most one request per endpoint plus history pages", async ({ page }) => {
  await signIn(page, "/rules");
  const counts = new Map<string, number>();
  page.on("request", (req) => {
    const u = new URL(req.url());
    if (u.pathname.startsWith("/api/v1/")) counts.set(u.pathname, (counts.get(u.pathname) ?? 0) + 1);
  });
  await page.getByRole("navigation", { name: "Primary" }).getByRole("link", { name: "Fleet" }).click();
  await expect(page.getByRole("img", { name: "compliant % over 90d" })).toBeVisible();
  await page.waitForTimeout(1500);
  for (const [path, n] of counts) {
    if (path === "/api/v1/compliance/history") continue;
    expect(n, path).toBeLessThanOrEqual(1);
  }
  expect(counts.get("/api/v1/compliance/history") ?? 0).toBeLessThanOrEqual(2);
});

test("a worst-rule link lands on the rule page, whose Findings button carries kind and rule", async ({ page }) => {
  await signIn(page);
  const worst = page.locator("section").filter({ has: page.getByRole("heading", { name: "Worst rules" }) });
  await expect(worst.getByRole("meter").first()).toBeVisible();
  await worst.getByRole("link").first().click();
  await expect(page).toHaveURL(/\/rules\/file\/[a-z]+$/);
  const findings = page.getByRole("link", { name: "Findings for this rule" });
  await expect(findings).toHaveAttribute("href", /\/findings\?kind=file&rule=[a-z]+$/);
  await findings.click();
  await expect(page).toHaveURL(/\/findings\?kind=file&rule=[a-z]+$/);
  await expect(page.getByRole("heading", { level: 1, name: "Findings" })).toBeVisible();
});

test("the rules page filters by kind tab", async ({ page }) => {
  await signIn(page, "/rules");
  await expect(page.getByRole("tab", { name: "All" })).toHaveAttribute("aria-selected", "true");
  await page.getByRole("tab", { name: "file" }).click();
  await expect(page).toHaveURL(/\/rules\?kind=file$/);
  await expect(page.getByRole("img", { name: "findings by rule and status" })).toBeVisible();
  await page.getByRole("tab", { name: "All" }).click();
  await expect(page).toHaveURL(/\/rules$/);
});

test("the orgs grid expands acme to its rules, hides the parked column and exports CSV", async ({ page }) => {
  await signIn(page, "/orgs");
  const grid = page.getByRole("table", { name: "Orgs" });
  await expect(grid).toBeVisible();
  await expect(grid.locator("tr[data-row=__total]")).toContainText("Total");

  // Expanding fetches /orgs/acme and lists its rules as child rows
  // linking to the rule page scoped to the org; the URL carries ?open=.
  const toggle = grid.getByRole("button", { name: /acme/ });
  await toggle.click();
  await expect(page).toHaveURL(/\/orgs\?open=acme$/);
  await expect(toggle).toHaveAttribute("aria-expanded", "true");
  const child = grid.locator("tr[data-children=acme]").getByRole("link").first();
  await expect(child).toHaveAttribute("href", /\/rules\/file\/[a-z]+\?org=acme$/);
  await page.getByRole("button", { name: "Collapse all" }).click();
  await expect(page).toHaveURL(/\/orgs$/);
  await expect(grid.locator("tr[data-children]")).toHaveCount(0);

  await page.getByRole("tab", { name: "Hide parked" }).click();
  await expect(page).toHaveURL(/\/orgs\?parked=hide$/);
  await expect(grid.getByRole("columnheader", { name: "Parked" })).toHaveCount(0);
  await page.getByRole("tab", { name: "All" }).click();
  await expect(grid.getByRole("columnheader", { name: "Parked" })).toBeVisible();

  const download = page.waitForEvent("download");
  await page.getByRole("button", { name: "Export CSV" }).click();
  const file = await download;
  expect(file.suggestedFilename()).toBe("orgs.csv");
  const text = (await (await file.createReadStream()).toArray()).join("");
  expect(text.split("\r\n")[0]).toBe("org,tracked,compliant,failing,parked");
  expect(text).toMatch(/\r\nacme,\d+,\d+,\d+,\d+\r\n/);
  expect(text).toMatch(/\r\ntotal,\d+,\d+,\d+,\d+\r\n$/);
});

test("the org detail shows KPIs, the trend and rule meters, and links to the org's findings", async ({ page }) => {
  await signIn(page, "/orgs/acme");
  await expect(page.getByRole("heading", { level: 1, name: /acme/ })).toBeVisible();
  await expect(page.getByRole("img", { name: "compliance % over 90d" })).toBeVisible();
  const rules = page.getByRole("table", { name: "Compliance by rule" });
  await expect(rules.getByRole("meter").first()).toBeVisible();
  await expect(rules.getByRole("link", { name: /^[a-z]+$/ }).first()).toHaveAttribute("href", /\/rules\/file\/[a-z]+\?org=acme$/);
  await expect(page.getByRole("link", { name: "Findings in this org" })).toHaveAttribute("href", /\/findings\?org=acme$/);
  await page.getByRole("tab", { name: "7d" }).click();
  await expect(page).toHaveURL(/\/orgs\/acme\?range=7d$/);
  await expect(page.getByRole("img", { name: "compliance % over 7d" })).toBeVisible();
});

test("without UI_DOCS_URL the sidebar has no Docs group", async ({ page }) => {
  await signIn(page);
  const nav = page.getByRole("navigation", { name: "Primary" });
  await expect(nav.getByRole("link", { name: /Fleet/ })).toBeVisible();
  await expect(nav.getByText("Docs", { exact: true })).toHaveCount(0);
  await expect(nav.getByRole("link", { name: "Documentation" })).toHaveCount(0);
});
