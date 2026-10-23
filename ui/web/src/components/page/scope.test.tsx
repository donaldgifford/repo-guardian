// The DOM must exist before radix and user-event load: keep this first.
import { withDOM } from "../../../testing/dom";

import { expect, test } from "bun:test";
import { QueryClientProvider } from "@tanstack/react-query";
import { RouterProvider } from "@tanstack/react-router";
import { render, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

import type { Schemas } from "../../api/client";
import { seededClient, testMe, testRouter } from "../../../testing/app";
import { OrgScope } from "./OrgScope";

withDOM();
const user = () => userEvent.setup({ pointerEventsCheck: 0, document });

const orgs = [{ org: "acme", tracked: 12 }, { org: "zeta", tracked: 3 }] as Schemas["OrgSummary"][];

function mount(initial: string, me: Schemas["Me"] = { ...testMe, orgs: ["acme", "zeta"] }) {
  const client = seededClient({ me, orgs: { items: orgs } });
  const router = testRouter(<OrgScope />, initial);
  const view = render(
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );
  return { router, ...view };
}

test("OrgScope lists every visible org and writes ?org= as a string", async () => {
  const u = user();
  const { router, findByRole, getByRole } = mount("/?days=7");
  const trigger = await findByRole("button", { name: "Org scope: All orgs (2)" });
  await u.click(trigger);
  const dialog = await findByRole("dialog", { name: "Org scope" });
  expect(dialog.textContent).toContain("12 tracked");
  await u.click(getByRole("button", { name: /^acme/ }));
  await waitFor(() => expect(router.state.location.search).toEqual({ org: "acme", days: 7 }));
  expect((await findByRole("button", { name: "Org scope: acme" })).textContent).toContain("acme");
});

test("choosing All orgs clears ?org=", async () => {
  const u = user();
  const { router, findByRole, getByRole } = mount("/?org=zeta");
  await u.click(await findByRole("button", { name: "Org scope: zeta" }));
  await u.click(getByRole("button", { name: "All orgs (2)" }));
  await waitFor(() => expect(router.state.location.search).toEqual({}));
});
