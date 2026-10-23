import { expect, type Page, test } from "@playwright/test";

// Sign-in, the session, the public status page and one spec per
// controls view (IMPL-0029 8.8). The caller (ada) sees acme only; the
// seed's globex must never appear.

// signIn runs the real code flow: the BFF redirects to the mock
// issuer, which approves at once and sends the browser back.
async function signIn(page: Page, path = "/") {
  await page.goto(path);
  await expect(page.getByRole("button", { name: "Account menu" })).toBeVisible();
}

test("an app path without a session goes through login and comes back", async ({ page }) => {
  await page.goto("/");
  await expect(page).toHaveURL(/\/$/);
  await expect(page.getByRole("heading", { level: 1, name: "Fleet" })).toBeVisible();
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
  await expect(nav.getByRole("link", { name: /Fleet/ })).toBeVisible();
  await expect(nav.getByText("Docs", { exact: true })).toHaveCount(0);
  await expect(nav.getByRole("link", { name: "Documentation" })).toHaveCount(0);
});

// expectNoGlobex fails if the out-of-scope org leaks into the page.
async function expectNoGlobex(page: Page) {
  await expect(page.locator("main")).not.toContainText(/globex/i);
}

test("Fleet totals the caller's orgs and links to controls and orgs", async ({ page }) => {
  await signIn(page);
  await expect(page.getByRole("heading", { level: 1, name: "Fleet" })).toBeVisible();
  // acme: web codeowners + renovate, api codeowners measured; 2 of 3 compliant.
  await expect(page.getByText("66.6%").first()).toBeVisible();
  const controls = page.getByRole("table", { name: "Controls" });
  await expect(controls.getByRole("link", { name: "codeowners" })).toBeVisible();
  await expect(page.getByRole("table", { name: "Orgs" }).getByRole("link", { name: "acme" })).toBeVisible();
  await expectNoGlobex(page);
  await controls.getByRole("link", { name: "renovate" }).click();
  await expect(page).toHaveURL(/\/controls\/renovate$/);
});

test("Controls lists each control and a control breaks down by org with versions", async ({ page }) => {
  await signIn(page, "/controls");
  await expect(page.getByRole("heading", { level: 1, name: "Controls" })).toBeVisible();
  await page.getByRole("table", { name: "Controls" }).getByRole("link", { name: "codeowners" }).click();
  await expect(page.getByRole("heading", { level: 1, name: /codeowners/ })).toBeVisible();
  const orgs = page.getByRole("table", { name: "Orgs" });
  await expect(orgs.getByRole("link", { name: "acme" })).toBeVisible();
  await expect(orgs.getByText(/v1 r-codeowners/)).toBeVisible();
  await expectNoGlobex(page);
});

test("an org shows baseline against org-added, its worst controls and exclusions", async ({ page }) => {
  await signIn(page, "/orgs");
  await page.getByRole("table", { name: "Orgs" }).getByRole("link", { name: "acme" }).click();
  await expect(page.getByRole("heading", { level: 1, name: "acme" })).toBeVisible();
  await expect(page.getByText("Baseline compliance")).toBeVisible();
  // Nothing org-added in acme: no data, never 0% or 100%.
  await expect(page.getByText("Org-added compliance").locator("..")).toContainText("no data");
  await expect(page.getByRole("table", { name: "Exclusions" })).toContainText("api pins its dependencies");
  await expect(page.getByRole("table", { name: "Worst controls" }).getByRole("link", { name: "renovate" })).toBeVisible();
  // An org outside the caller's scope reads as not found.
  await page.goto("/orgs/globex");
  await expect(page.getByRole("alert")).toBeVisible();
});

test("a repository shows its controls with evidence, the evaluation log and events", async ({ page }) => {
  await signIn(page, "/repositories?org=acme");
  await page.getByRole("table", { name: "Repositories" }).getByRole("link", { name: "acme/web" }).click();
  await expect(page.getByRole("heading", { level: 1, name: "acme/web" })).toBeVisible();
  const renovate = page.locator('[data-control="renovate"]');
  await expect(renovate).toContainText("non compliant");
  await expect(renovate).toContainText("dependency_updates.exists");
  await expect(renovate).toContainText("path: renovate.json");

  // INV-0023 F2: the crumb names the repository, never its id.
  const crumb = page.getByRole("navigation", { name: "Breadcrumb" });
  await expect(crumb.locator('b[aria-current="page"]')).toHaveText("acme/web");

  // INV-0023 F1: a failing rule carries the bad tone, not mute.
  const failing = page.getByRole("table", { name: "renovate rules" }).getByText("fail", { exact: true });
  await expect(failing).toHaveClass(/--color-bad/);

  // No Temporal client on the e2e api: the POST answers 501 and the
  // button goes away.
  const button = page.getByRole("button", { name: "Re-evaluate" });
  await button.click();
  await expect(button).toHaveCount(0);

  await page.getByRole("tab", { name: "Evaluation log" }).click();
  const log = page.getByRole("table", { name: "Evaluation log" });
  await expect(log).toContainText("error");
  await expect(log).toContainText("502 Bad Gateway");

  await page.getByRole("tab", { name: "Events" }).click();
  await expect(page.getByRole("table", { name: "Events" })).toContainText("renovate");
});

test("History plots one control over the selected range", async ({ page }) => {
  await signIn(page, "/history");
  await expect(page.getByRole("heading", { level: 1, name: "History" })).toBeVisible();
  await page.getByRole("combobox", { name: "Control" }).selectOption("codeowners");
  await expect(page).toHaveURL(/control=codeowners/);
  await expect(page.getByRole("img", { name: "Compliance over time" })).toBeVisible();
  await page.getByRole("tab", { name: "7d" }).click();
  await expect(page).toHaveURL(/days=7/);
});

// The enterprise layer is for all-org callers only, and the catalogue
// carries just what the visible layers reference: acme's renovate
// exclusion, not the baseline's codeowners.
test("Policy shows the caller's orgs only, and no enterprise layer", async ({ page }) => {
  await signIn(page, "/policy");
  await expect(page.getByRole("heading", { level: 1, name: "Policy" })).toBeVisible();
  await expect(page.locator('[data-org="acme"]')).toContainText("api pins its dependencies");
  await expect(page.getByRole("table", { name: "Catalogue" })).toContainText("renovate@1");
  await expect(page.getByText("Enterprise baseline")).toHaveCount(0);
  await expectNoGlobex(page);
});

test("the status page shows topology, Apps and the evaluation strip", async ({ page }) => {
  await page.goto("/status");
  await expect(page.getByTestId("status-topology")).toHaveText("topology split");
  await expect(page.locator('[data-app="eval"]')).toContainText("2 installations");
  await expect(page.locator('[data-app="remediate"]')).toContainText("1 installations");
  await expect(page.getByText("Overdue evaluations")).toBeVisible();
  await expect(page.getByTestId("status-coverage")).toBeVisible();
});

// The keyboard pass (IMPL-0031 6.3, carried by IMPL-0032 2.7): Tab walks
// the top bar, the sidebar, the breadcrumb and the page header in
// reading order, the focused element shows the one focus ring, and
// Escape closes a popover and hands focus back to its opener.
test("Tab walks Fleet in reading order with a visible ring, and Escape closes the org scope", async ({ page }) => {
  await signIn(page);
  const scope = page.getByRole("button", { name: /^Org scope/ });
  await expect(scope).toBeVisible();
  const seen: string[] = [];
  for (let i = 0; i < 30; i++) {
    await page.keyboard.press("Tab");
    seen.push(
      await page.evaluate(() => {
        const el = document.activeElement as HTMLElement | null;
        return el ? (el.getAttribute("aria-label") ?? el.textContent?.trim() ?? "") : "";
      }),
    );
  }
  const expected = [/^Search/, /^Account menu$/, /^Fleet$/, /^Status page$/, /^Repo Guardian$/, /^Org scope/, /^30d$/];
  let at = 0;
  for (const want of expected) {
    const i = seen.findIndex((s, j) => j >= at && want.test(s));
    expect(i, `${want} after ${seen.slice(0, at).join(" › ")}`).toBeGreaterThanOrEqual(at);
    at = i + 1;
  }

  // Only the selected range tab is a Tab stop; arrows move along the row.
  await scope.focus();
  await page.keyboard.press("Tab");
  await expect(page.getByRole("tab", { name: "30d" })).toBeFocused();
  const ring = await page.evaluate(() => {
    const s = getComputedStyle(document.activeElement as Element);
    return [s.outlineStyle, s.outlineWidth];
  });
  expect(ring).toEqual(["solid", "2px"]);
  await page.keyboard.press("ArrowRight");
  await expect(page.getByRole("tab", { name: "90d" })).toHaveAttribute("aria-selected", "true");
  await expect(page).toHaveURL(/days=90/);
  await page.keyboard.press("ArrowLeft");
  await expect(page).not.toHaveURL(/days=/);

  // The org-scope popover opens from the keyboard, and Escape closes it
  // and returns focus to the opener.
  await scope.focus();
  await page.keyboard.press("Enter");
  await expect(page.getByRole("dialog", { name: "Org scope" })).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(page.getByRole("dialog", { name: "Org scope" })).toHaveCount(0);
  await expect(scope).toBeFocused();
});

test("Findings lists the non-compliant results, M matches the nav badge, and the drawer deep link survives a reload", async ({ page }) => {
  await signIn(page, "/findings");
  await expect(page.getByRole("heading", { level: 1, name: "Findings" })).toBeVisible();
  const table = page.getByRole("table", { name: "Findings" });
  const row = table.getByRole("row").filter({ hasText: "renovate" });
  await expect(row).toHaveCount(1);
  await expect(row).toContainText("web");
  await expect(row).toContainText("exists");
  // Only the non-compliant bucket: acme/web's compliant codeowners is not a finding.
  await expect(table.getByRole("row").filter({ hasText: "codeowners" })).toHaveCount(0);

  // M equals the nav badge (both the non-compliant count in scope).
  const count = page.getByText(/^\d+ loaded of \d+$/);
  await expect(count).toBeVisible();
  const m = /of (\d+)$/.exec((await count.textContent()) ?? "")?.[1];
  const nav = page.getByRole("navigation", { name: "Primary" }).getByRole("link", { name: /^Findings/ });
  await expect(nav.locator("em")).toHaveText(m ?? "");

  // The filter chips write the URL.
  await page.getByRole("button", { name: "Add filter" }).click();
  await page.getByRole("button", { name: "control", exact: true }).click();
  await page.getByRole("button", { name: "renovate" }).click();
  await expect(page).toHaveURL(/control=renovate/);
  await expect(page.getByRole("button", { name: "control filter: renovate" })).toBeVisible();

  // A row opens the drawer on ?open=<repository_id>/<control>; the URL
  // alone restores it.
  await row.click();
  await expect(page).toHaveURL(/open=\d+%2Frenovate|open=\d+\/renovate/);
  const drawer = page.getByRole("dialog", { name: /^Finding: acme\/web renovate/ });
  await expect(drawer).toBeVisible();
  await expect(drawer.getByRole("table", { name: "renovate rules" })).toContainText("fail");
  await expect(drawer).toContainText("path: renovate.json");
  await page.reload();
  const again = page.getByRole("dialog", { name: /^Finding: acme\/web renovate/ });
  await expect(again).toBeVisible();
  // No Temporal client on the e2e api: the POST answers 501 and the
  // button goes away, as on the repository page.
  const button = again.getByRole("button", { name: "Re-evaluate" });
  await button.click();
  await expect(button).toHaveCount(0);
  await page.keyboard.press("Escape");
  await expect(page.getByRole("dialog", { name: /^Finding:/ })).toHaveCount(0);
  await expect(page).not.toHaveURL(/open=/);
  await expectNoGlobex(page);
});

test("a control's Oldest findings opens the findings drawer", async ({ page }) => {
  await signIn(page, "/controls/renovate");
  await expect(page.getByRole("heading", { level: 1, name: /renovate/ })).toBeVisible();
  await expect(page.getByRole("list", { name: "renovate findings by failing rule" }).getByRole("link", { name: "exists" })).toHaveAttribute("href", /\/findings\?control=renovate&rule=exists/);
  await page.getByRole("list", { name: "Oldest findings" }).getByRole("link", { name: /acme\/web/ }).click();
  await expect(page).toHaveURL(/\/findings\?/);
  await expect(page.getByRole("dialog", { name: /^Finding: acme\/web renovate/ })).toBeVisible();
  await expectNoGlobex(page);
});

test("the orgs tree expands an org into aligned repository rows, sums the footer and keeps its state in the URL", async ({ page }) => {
  await signIn(page, "/orgs");
  await expect(page.getByRole("heading", { level: 1, name: "Orgs" })).toBeVisible();
  const grid = page.getByRole("table", { name: "Orgs" });
  const acme = grid.locator("tr[data-row=acme]");
  await expect(acme).toContainText("2"); // tracked
  await expect(acme.locator("td[data-heat=compliant]")).toContainText(/\d+%/);
  // The footer sums the visible org rows: ada sees acme alone.
  const footer = grid.locator("tr[data-row=__footer]");
  await expect(footer).toContainText("1 orgs");
  const orgs = (await (await page.request.get("/api/v1/orgs")).json()) as { items: { org: string; tracked: number; parked: number; compliance: { compliant: number; non_compliant: number } }[] };
  const acmeApi = orgs.items.find((o) => o.org === "acme")!;
  await expect(footer.locator("td").nth(1)).toHaveText(String(acmeApi.tracked));
  await expect(footer.locator("td").nth(2)).toContainText(String(acmeApi.compliance.compliant));
  await expect(footer.locator("td").nth(3)).toContainText(String(acmeApi.compliance.non_compliant));
  await expect(footer.locator("td").nth(5)).toHaveText(String(acmeApi.parked));

  await grid.getByRole("button", { name: "Expand acme" }).click();
  await expect(page).toHaveURL(/open=acme/);
  const web = grid.locator("tr[data-child='acme/web']");
  await expect(web).toBeVisible();
  // Same body, every column: a child row has as many cells as the org row.
  expect(await web.locator("td").count()).toBe(await acme.locator("td").count());
  await expect(web).toContainText("50%"); // 1 compliant of 2 measured
  // An unmeasured cell reads no data, never a percentage.
  const api = grid.locator("tr[data-child='acme/api']");
  await expect(api).toBeVisible();
  // No colour disagrees with its percentage: every shaded cell shows one.
  for (const cell of await grid.locator("td[data-heat]").all()) {
    await expect(cell).toContainText(/\d+%/);
  }
  await expect(grid.locator("td[data-measured=false]")).toHaveCount(0);

  // The filter narrows the loaded children.
  await page.getByRole("textbox", { name: "Filter repositories" }).fill("we");
  await expect(page).toHaveURL(/q=we/);
  await expect(grid.locator("tr[data-child='acme/api']")).toHaveCount(0);
  await expect(web).toBeVisible();

  // The Parked tab lists parked repositories with the reason; the open
  // set carries over, so acme is already expanded there.
  await page.getByRole("textbox", { name: "Filter repositories" }).fill("");
  await expect(page).not.toHaveURL(/q=/);
  await page.getByRole("tab", { name: "Parked" }).click();
  await expect(page).toHaveURL(/tab=parked/);
  const parked = page.getByRole("table", { name: "Parked repositories by org" });
  await expect(parked.getByRole("button", { name: "Collapse acme" })).toBeVisible();
  await expect(parked.locator("tr[data-child='acme/legacy']")).toContainText("archived");

  // PR activity is gated: the e2e deployment has the Remediation App.
  await page.getByRole("tab", { name: "PR activity" }).click();
  await expect(page.getByRole("table", { name: "PR activity" }).or(page.getByText("No open pull requests."))).toBeVisible();
  await expectNoGlobex(page);
});
