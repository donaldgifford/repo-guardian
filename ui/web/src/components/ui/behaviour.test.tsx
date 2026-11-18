// The DOM must exist before radix and user-event load: keep this first.
import { withDOM } from "../../../testing/dom";

import { describe, expect, test } from "bun:test";
import { createMemoryHistory, createRootRoute, createRoute, createRouter, RouterProvider } from "@tanstack/react-router";
import { act, cleanup, render, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";

import { Command } from "./command";
import { Drawer, DrawerContent, DrawerHeader, DrawerTrigger } from "./drawer";
import { nextTab, SearchTabs, Tabs } from "./tabs";

withDOM(cleanup);

// Radix sets pointer-events: none on the body behind a modal; happy-dom
// has no layout, so the check is switched off rather than modelled.
// user-event captures globalThis.document when imported, before withDOM
// registers one, so the document is passed in.
const user = () => userEvent.setup({ pointerEventsCheck: 0, document });

describe("Drawer", () => {
  function Harness() {
    const [open, setOpen] = useState(false);
    return (
      <Drawer open={open} onOpenChange={setOpen}>
        <DrawerTrigger>open</DrawerTrigger>
        <DrawerContent>
          <DrawerHeader title="acme/web" subtitle="rule renovate (file)" />
          <button type="button">first</button>
          <button type="button">last</button>
        </DrawerContent>
      </Drawer>
    );
  }

  test("moves focus in, traps it, and Escape closes and returns focus to the opener", async () => {
    const u = user();
    const { getByText, queryByRole, getByRole } = render(<Harness />);
    const opener = getByText("open");
    await u.click(opener);
    const dialog = getByRole("dialog");
    expect(dialog).toBeTruthy();
    expect(dialog.contains(document.activeElement)).toBe(true);

    // Tab cycles inside the dialog: Close, first, last, then back.
    for (let i = 0; i < 6; i++) {
      await u.tab();
      expect(dialog.contains(document.activeElement)).toBe(true);
    }

    await u.keyboard("{Escape}");
    await waitFor(() => expect(queryByRole("dialog")).toBeNull());
    expect(document.activeElement).toBe(opener);
  });

  test("the close button closes it", async () => {
    const u = user();
    const { getByText, getByLabelText, queryByRole } = render(<Harness />);
    await u.click(getByText("open"));
    await u.click(getByLabelText("Close"));
    await waitFor(() => expect(queryByRole("dialog")).toBeNull());
  });
});

test("SearchTabs write the search param and drop it for the default", async () => {
  const u = user();
  const rootRoute = createRootRoute();
  const indexRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: "/",
    validateSearch: (s: Record<string, unknown>) => (typeof s.range === "string" ? { range: s.range } : {}),
    component: () => (
      <SearchTabs
        label="Range"
        param="range"
        defaultValue="30d"
        options={[
          { value: "7d", label: "7d" },
          { value: "30d", label: "30d" },
        ]}
      />
    ),
  });
  const router = createRouter({ routeTree: rootRoute.addChildren([indexRoute]), history: createMemoryHistory({ initialEntries: ["/"] }) });
  const { findByRole, getByRole } = render(<RouterProvider router={router} />);

  const seven = await findByRole("tab", { name: "7d" });
  expect(getByRole("tab", { name: "30d" }).getAttribute("aria-selected")).toBe("true");
  await u.click(seven);
  await waitFor(() => expect(router.state.location.search).toEqual({ range: "7d" }));
  await waitFor(() => expect(getByRole("tab", { name: "7d" }).getAttribute("aria-selected")).toBe("true"));

  await u.click(getByRole("tab", { name: "30d" }));
  await waitFor(() => expect(router.state.location.search).toEqual({}));
});

test("Tabs are one Tab stop: arrows move selection and focus along the row, Home and End jump", async () => {
  const u = user();
  function Host() {
    const [value, setValue] = useState("7d");
    return (
      <Tabs
        label="Range"
        value={value}
        onChange={setValue}
        options={[
          { value: "1d", label: "1d" },
          { value: "7d", label: "7d" },
          { value: "30d", label: "30d" },
        ]}
      />
    );
  }
  const { getByRole } = render(<Host />);
  const seven = getByRole("tab", { name: "7d" });
  expect(getByRole("tab", { name: "1d" }).getAttribute("tabindex")).toBe("-1");
  await act(async () => seven.focus());
  await u.keyboard("{ArrowRight}");
  await waitFor(() => expect(getByRole("tab", { name: "30d" }).getAttribute("aria-selected")).toBe("true"));
  expect(document.activeElement).toBe(getByRole("tab", { name: "30d" }));
  await u.keyboard("{ArrowRight}");
  await waitFor(() => expect(getByRole("tab", { name: "1d" }).getAttribute("aria-selected")).toBe("true"));
  await u.keyboard("{End}");
  await waitFor(() => expect(getByRole("tab", { name: "30d" }).getAttribute("aria-selected")).toBe("true"));
  await u.keyboard("{Home}");
  await waitFor(() => expect(getByRole("tab", { name: "1d" }).getAttribute("aria-selected")).toBe("true"));
  expect(nextTab([{ value: "a", label: "a" }], "a", "ArrowRight")).toBe("a");
  expect(nextTab([{ value: "a", label: "a" }], "a", "Enter")).toBeUndefined();
});

test("Command moves the active row with arrows and selects on Enter", async () => {
  const u = user();
  const picked: string[] = [];
  const groups = [
    { heading: "Repositories", items: [{ id: "r1", label: "acme/web", onSelect: () => picked.push("r1") }] },
    { heading: "Rules", items: [{ id: "k1", label: "renovate", detail: "file", onSelect: () => picked.push("k1") }] },
  ];
  const { getByRole, getAllByRole } = render(<Command query="" onQueryChange={() => {}} groups={groups} />);
  const input = getByRole("combobox");
  await act(async () => input.focus());
  const options = getAllByRole("option");
  expect(options[0]?.getAttribute("aria-selected")).toBe("true");
  await u.keyboard("{ArrowDown}");
  expect(options[1]?.getAttribute("aria-selected")).toBe("true");
  expect(input.getAttribute("aria-activedescendant")).toBe(options[1]?.id ?? "");
  await u.keyboard("{Enter}");
  expect(picked).toEqual(["k1"]);
  await u.keyboard("{ArrowDown}");
  expect(options[0]?.getAttribute("aria-selected")).toBe("true");
});
