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

const rules = [
  { kind: "file", name: "renovate", counts: { compliant: 1, non_compliant: 0, not_applicable: 0, unknown: 0 }, compliant_percent: 100 },
  { kind: "file", name: "codeowners", counts: { compliant: 1, non_compliant: 0, not_applicable: 0, unknown: 0 }, compliant_percent: 100 },
] as Schemas["RuleCompliance"][];
const orgs = [{ org: "acme", tracked: 12, parked: 1, counts: { compliant: 1, non_compliant: 0, not_applicable: 0, unknown: 0 }, compliant_percent: 100 }] as Schemas["OrgSummary"][];
const repo = { id: 7, org: "acme", name: "web", installation_id: 1, active: true, discovered_at: "2026-01-01T00:00:00Z", last_check_outcome: "success", policy_version: "v2:x" } as Schemas["Repository"];

describe("paletteGroups", () => {
  const go = { repository: () => {}, rule: () => {}, org: () => {} };

  test("filters rules and orgs by the query and keeps the API's repositories", () => {
    const g = paletteGroups("ren", { repositories: [repo], rules, orgs }, go);
    expect(g.map((x) => [x.heading, x.items.map((i) => i.id)])).toEqual([
      ["Repositories", ["repo-7"]],
      ["Rules", ["rule-file-renovate"]],
      ["Orgs", []],
    ]);
  });

  test("an empty query lists every rule and org", () => {
    const g = paletteGroups("", { repositories: [], rules, orgs }, go);
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
  const client = seededClient({ rules: { items: rules }, orgs: { items: orgs } });
  client.setQueryData(["repository-search", "web", "acme"], { items: [repo] });
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
  const { router, findByRole, queryByRole, getByRole } = mount("/findings?org=acme");
  await findByRole("button", { name: "search" });
  expect(queryByRole("dialog")).toBeNull();

  await u.keyboard("{Meta>}k{/Meta}");
  const input = await findByRole("combobox");
  expect(input.getAttribute("placeholder")).toContain("Search repositories");

  await u.type(input, "web");
  const option = await findByRole("option", { name: /acme\/web/ });
  expect(option).toBeTruthy();
  await u.keyboard("{Enter}");
  await waitFor(() => expect(router.state.location.pathname).toBe("/repos/7"));
  expect(router.state.location.search).toEqual({ org: "acme" });
  await waitFor(() => expect(queryByRole("dialog")).toBeNull());
  expect(getByRole("button", { name: "search" })).toBeTruthy();
});

test("Escape closes it and a rule row navigates to the rule", async () => {
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
  await waitFor(() => expect(router.state.location.pathname).toBe("/rules/file/codeowners"));
});
