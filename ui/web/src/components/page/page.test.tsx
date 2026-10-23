// The DOM must exist before radix and user-event load: keep this first.
import { withDOM } from "../../../testing/dom";

import { describe, expect, test } from "bun:test";
import { RouterProvider } from "@tanstack/react-router";
import { render } from "@testing-library/react";
import { renderToStaticMarkup } from "react-dom/server";

import { testRouter } from "../../../testing/app";
import { KindBadge } from "../badges";
import { BreadcrumbList, crumbsFor } from "./Breadcrumbs";
import { PageHeader } from "./PageHeader";

withDOM();

describe("crumbsFor", () => {
  test.each([
    ["fleet", { routeId: "/app/" }, ["Repo Guardian", "Fleet"]],
    ["section", { routeId: "/app/controls" }, ["Repo Guardian", "Controls"]],
    ["item", { routeId: "/app/orgs/$org", params: { org: "acme" } }, ["Repo Guardian", "Orgs", "acme"]],
    ["item without its param", { routeId: "/app/controls/$id", params: {} }, ["Repo Guardian", "Controls"]],
    ["numeric item while loading", { routeId: "/app/repositories/$id", params: { id: "1" } }, ["Repo Guardian", "Repositories", "…"]],
    ["remediations list", { routeId: "/app/remediations", search: {} }, ["Repo Guardian", "Remediations"]],
    ["open remediation while loading", { routeId: "/app/remediations", search: { open: 12 } }, ["Repo Guardian", "Remediations", "…"]],
    ["open held result", { routeId: "/app/remediations", search: { open: "7/renovate" } }, ["Repo Guardian", "Remediations", "7/renovate"]],
    ["unknown route", { routeId: "/nope" }, ["Repo Guardian"]],
    ["no match", undefined, ["Repo Guardian"]],
  ])("%s", (_name, match, want) => {
    expect(crumbsFor(match).map((c) => c.label)).toEqual(want);
  });
});

describe("crumbsFor with a view label (INV-0023 F2)", () => {
  const match = { routeId: "/app/repositories/$id", params: { id: "1" } };

  test("reads the label, never the id", () => {
    const labels = crumbsFor(match, { param: "1", label: "acme/widgets" }).map((c) => c.label);
    expect(labels).toEqual(["Repo Guardian", "Repositories", "acme/widgets"]);
    expect(labels).not.toContain("1");
  });

  test("ignores a label left from another item", () => {
    expect(crumbsFor(match, { param: "2", label: "acme/gadgets" }).at(-1)?.label).toBe("…");
  });

  test("the open remediation reads org/name · control", () => {
    const open = { routeId: "/app/remediations", search: { open: 12 } };
    expect(crumbsFor(open, { param: "12", label: "acme/web · renovate" }).at(-1)?.label).toBe("acme/web · renovate");
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
  const router = testRouter(<BreadcrumbList crumbs={[{ label: "Repo Guardian", to: "/" }, { label: "Status", to: "/status" }, { label: "renovate" }]} />, "/status");
  const { findByRole, getByRole } = render(<RouterProvider router={router} />);
  const nav = await findByRole("navigation", { name: "Breadcrumb" });
  expect(nav).toBeTruthy();
  expect(getByRole("link", { name: "Repo Guardian" }).getAttribute("href")).toBe("/");
  const leaf = nav.querySelector("b[aria-current=page]");
  expect(leaf?.textContent).toBe("renovate");
});
