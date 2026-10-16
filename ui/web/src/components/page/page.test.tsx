// The DOM must exist before radix and user-event load: keep this first.
import { withDOM } from "../../../testing/dom";

import { describe, expect, test } from "bun:test";
import { QueryClientProvider } from "@tanstack/react-query";
import { RouterProvider } from "@tanstack/react-router";
import { render, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderToStaticMarkup } from "react-dom/server";

import type { Schemas } from "../../api/client";
import { seededClient, testMe, testRouter } from "../../../testing/app";
import { KindBadge } from "../badges";
import { BreadcrumbList, crumbsFor } from "./Breadcrumbs";
import { OrgScope } from "./OrgScope";
import { PageHeader } from "./PageHeader";
import { RangeTabs } from "./RangeTabs";

withDOM();
const user = () => userEvent.setup({ pointerEventsCheck: 0, document });

describe("crumbsFor", () => {
  const m = (routeId: string, params: Record<string, unknown> = {}, search: Record<string, unknown> = {}) => ({ routeId, params, search });

  test.each([
    ["fleet", m("/app/"), ["Repo Guardian", "Fleet"]],
    ["rules list", m("/app/rules"), ["Repo Guardian", "Rules"]],
    ["rule", m("/app/rules/$kind/$name", { kind: "file", name: "renovate" }), ["Repo Guardian", "Rules", "renovate"]],
    ["org", m("/app/orgs/$org", { org: "acme" }), ["Repo Guardian", "Orgs", "acme"]],
    ["findings", m("/app/findings"), ["Repo Guardian", "Findings"]],
    ["findings drawer", m("/app/findings", {}, { open: "1001/file/renovate" }), ["Repo Guardian", "Findings", "rule renovate (file)"]],
    ["repository without data", m("/app/repos/$id", { id: "7" }), ["Repo Guardian", "Repositories", "#7"]],
    ["unknown route", m("/nope"), ["Repo Guardian"]],
    ["no match", undefined, ["Repo Guardian"]],
  ])("%s", (_name, match, want) => {
    expect(crumbsFor(match).map((c) => c.label)).toEqual(want);
  });

  test("a repository uses its cached name", () => {
    expect(crumbsFor(m("/app/repos/$id", { id: "7" }), "acme/web").map((c) => c.label)).toEqual(["Repo Guardian", "Repositories", "acme/web"]);
  });
});

test("PageHeader renders title, badge, subtitle code and controls", () => {
  const html = renderToStaticMarkup(
    <PageHeader
      title="renovate"
      badge={<KindBadge kind="file" />}
      subtitle={
        <>
          From <code>/rules/file/renovate</code>.
        </>
      }
      controls={<button type="button">Export</button>}
    />,
  );
  expect(html).toMatch(/<h1[^>]*><span[^>]*>renovate<\/span><span[^>]*>file<\/span><\/h1>/);
  expect(html).toContain("<code>/rules/file/renovate</code>");
  expect(html).toContain(">Export</button>");
});

test("BreadcrumbList marks the leaf current and links the rest", async () => {
  const router = testRouter(<BreadcrumbList crumbs={[{ label: "Repo Guardian", to: "/" }, { label: "Rules", to: "/rules" }, { label: "renovate" }]} />, "/rules");
  const { findByRole, getByRole } = render(<RouterProvider router={router} />);
  const nav = await findByRole("navigation", { name: "Breadcrumb" });
  expect(nav).toBeTruthy();
  expect(getByRole("link", { name: "Repo Guardian" }).getAttribute("href")).toBe("/");
  const leaf = nav.querySelector("b[aria-current=page]");
  expect(leaf?.textContent).toBe("renovate");
});

const orgs = [
  { org: "acme", tracked: 12, parked: 1, counts: { compliant: 1, non_compliant: 0, not_applicable: 0, unknown: 0 }, compliant_percent: 100 },
  { org: "globex", tracked: 3, parked: 0, counts: { compliant: 1, non_compliant: 0, not_applicable: 0, unknown: 0 }, compliant_percent: 100 },
] as Schemas["OrgSummary"][];

function mountScope(initial: string, me: Schemas["Me"] = testMe) {
  const client = seededClient({ orgs: { items: orgs } });
  client.setQueryData(["me"], me);
  const router = testRouter(
    <>
      <OrgScope />
      <RangeTabs />
    </>,
    initial,
  );
  const view = render(
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );
  return { router, ...view };
}

test("OrgScope lists All orgs (N) with the visible orgs and writes ?org=", async () => {
  const u = user();
  const { router, findByRole, getByRole, findByText } = mountScope("/findings?status=non_compliant");
  const trigger = await findByRole("button", { name: /^Org scope/ });
  expect(trigger.textContent).toContain("All orgs (1)");

  await u.click(trigger);
  await findByText("12 tracked");
  expect(getByRole("button", { name: /acme/ })).toBeTruthy();
  await u.click(getByRole("button", { name: /acme/ }));
  await waitFor(() => expect(router.state.location.search).toEqual({ status: "non_compliant", org: "acme" }));
  await waitFor(() => expect(getByRole("button", { name: /^Org scope/ }).textContent).toContain("acme"));

  await u.click(getByRole("button", { name: /^Org scope/ }));
  await u.click(await findByRole("button", { name: /All orgs/ }));
  await waitFor(() => expect(router.state.location.search).toEqual({ status: "non_compliant" }));
});

test("an all_orgs caller counts every org the API returned", async () => {
  const { findByRole } = mountScope("/", { ...testMe, all_orgs: true, orgs: [] });
  const trigger = await findByRole("button", { name: /^Org scope/ });
  expect(trigger.textContent).toContain("All orgs (2)");
});

test("RangeTabs defaults to 90d and writes the others to ?range=", async () => {
  const u = user();
  const { router, findByRole, getByRole } = mountScope("/");
  const seven = await findByRole("tab", { name: "7d" });
  expect(getByRole("tab", { name: "90d" }).getAttribute("aria-selected")).toBe("true");
  await u.click(seven);
  await waitFor(() => expect(router.state.location.search).toEqual({ range: "7d" }));
  await u.click(getByRole("tab", { name: "90d" }));
  await waitFor(() => expect(router.state.location.search).toEqual({}));
});
