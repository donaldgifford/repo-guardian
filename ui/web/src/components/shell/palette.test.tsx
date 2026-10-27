// The DOM must exist before radix and user-event load: keep this first.
import { withDOM } from "../../../testing/dom";

import { describe, expect, test } from "bun:test";
import { QueryClientProvider } from "@tanstack/react-query";
import { RouterProvider } from "@tanstack/react-router";
import { render, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";

import type { Schemas } from "../../api/client";
import { seededClient, testRouter } from "../../../testing/app";
import { CommandPalette, paletteGroups } from "./CommandPalette";

withDOM();
const user = () => userEvent.setup({ pointerEventsCheck: 0, document });

const controls = [
  { id: "renovate", title: "Renovate config", type: "file" },
  { id: "codeowners", title: "CODEOWNERS", type: "codeowners" },
] as Schemas["ControlSummary"][];
const orgs = [{ org: "acme", tracked: 12 }] as Schemas["OrgSummary"][];
const repo = { id: 7, org: "acme", name: "web", active: true, policy_state: "managed" } as Schemas["Repository"];
const parked = { id: 8, org: "acme", name: "old", active: false, park_reason: "archived" } as Schemas["Repository"];

describe("paletteGroups", () => {
  const go = { repository: () => {}, control: () => {}, org: () => {} };

  test("filters controls and orgs by the query and keeps the API's repositories", () => {
    const g = paletteGroups("ren", { repositories: [repo], controls, orgs }, go);
    expect(g.map((x) => [x.heading, x.items.map((i) => i.id)])).toEqual([
      ["Repositories", ["repo-7"]],
      ["Controls", ["control-renovate"]],
      ["Orgs", []],
    ]);
  });

  test("a control matches on its title too", () => {
    const g = paletteGroups("owners", { repositories: [], controls, orgs }, go);
    expect(g[1]?.items.map((i) => i.id)).toEqual(["control-codeowners"]);
  });

  test("an empty query lists every control and org, and a parked repository says so", () => {
    const g = paletteGroups("", { repositories: [parked], controls, orgs }, go);
    expect(g[0]?.items[0]?.detail).toBe("parked · archived");
    expect(g[1]?.items).toHaveLength(2);
    expect(g[2]?.items[0]?.detail).toBe("12 tracked");
  });
});

function PaletteHost() {
  const [open, setOpen] = useState(false);
  return (
    <>
      <button type="button" onClick={() => setOpen(true)}>
        search
      </button>
      <CommandPalette open={open} onOpenChange={setOpen} />
    </>
  );
}

function mount(initial: string) {
  const client = seededClient({ orgs: { items: orgs } });
  client.setQueryData(["controls", ""], { items: controls });
  client.setQueryData(["repositories", { q: "web", org: "acme" }], { pages: [{ items: [repo] }], pageParams: [undefined] });
  const router = testRouter(<PaletteHost />, initial);
  const view = render(
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );
  return { router, ...view };
}

test("⌘K opens the palette, typing finds the repository, Enter navigates with ?org= kept", async () => {
  const u = user();
  const { router, findByRole, queryByRole, getByRole } = mount("/controls?org=acme");
  await findByRole("button", { name: "search" });
  expect(queryByRole("dialog")).toBeNull();

  await u.keyboard("{Meta>}k{/Meta}");
  const input = await findByRole("combobox");
  expect(input.getAttribute("placeholder")).toContain("Search repositories");

  await u.type(input, "web");
  const option = await findByRole("option", { name: /acme\/web/ });
  expect(option).toBeTruthy();
  await u.keyboard("{Enter}");
  await waitFor(() => expect(router.state.location.pathname).toBe("/repositories/7"));
  expect(router.state.location.search).toEqual({ org: "acme" });
  await waitFor(() => expect(queryByRole("dialog")).toBeNull());
  expect(getByRole("button", { name: "search" })).toBeTruthy();
});

test("Escape closes it and a control row navigates to the control", async () => {
  const u = user();
  const { router, findByRole, queryByRole, getByText } = mount("/");
  await u.click(await findByRole("button", { name: "search" }));
  await findByRole("combobox");
  await u.keyboard("{Escape}");
  await waitFor(() => expect(queryByRole("dialog")).toBeNull());

  await u.click(getByText("search"));
  const input = await findByRole("combobox");
  await u.type(input, "code");
  await u.click(await findByRole("option", { name: /codeowners/ }));
  await waitFor(() => expect(router.state.location.pathname).toBe("/controls/codeowners"));
});
