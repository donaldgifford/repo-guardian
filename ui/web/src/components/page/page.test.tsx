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
    ["overview", { routeId: "/app/" }, ["Repo Guardian", "Overview"]],
    ["unknown route", { routeId: "/nope" }, ["Repo Guardian"]],
    ["no match", undefined, ["Repo Guardian"]],
  ])("%s", (_name, match, want) => {
    expect(crumbsFor(match).map((c) => c.label)).toEqual(want);
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
