// The DOM must exist before radix and user-event load: keep this first.
import { withDOM } from "../../../testing/dom";

import { describe, expect, test } from "bun:test";
import { createMemoryHistory, createRootRoute, createRoute, createRouter, RouterProvider } from "@tanstack/react-router";
import { act, render, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";

import { Chip } from "./chip";
import { Command } from "./command";
import { Drawer, DrawerContent, DrawerHeader, DrawerTrigger } from "./drawer";
import { Pill, PillGroup } from "./pill";
import { Popover, PopoverContent, PopoverTrigger } from "./popover";
import { SearchTabs } from "./tabs";

withDOM();

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

test("Pill toggles aria-checked", async () => {
  const u = user();
  function Harness() {
    const [sel, setSel] = useState("a");
    return (
      <PillGroup label="Series">
        <Pill color="red" label="A" checked={sel === "a"} onSelect={() => setSel("a")} />
        <Pill color="blue" label="B" checked={sel === "b"} onSelect={() => setSel("b")} />
      </PillGroup>
    );
  }
  const { getByRole } = render(<Harness />);
  expect(getByRole("radio", { name: "A" }).getAttribute("aria-checked")).toBe("true");
  expect(getByRole("radio", { name: "B" }).getAttribute("aria-checked")).toBe("false");
  await u.click(getByRole("radio", { name: "B" }));
  expect(getByRole("radio", { name: "A" }).getAttribute("aria-checked")).toBe("false");
  expect(getByRole("radio", { name: "B" }).getAttribute("aria-checked")).toBe("true");
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

test("Chip opens its popover editor", async () => {
  const u = user();
  const { getByRole, queryByText, findByText } = render(
    <Popover>
      <PopoverTrigger asChild>
        <Chip name="status" value="non_compliant" />
      </PopoverTrigger>
      <PopoverContent>editor</PopoverContent>
    </Popover>,
  );
  expect(queryByText("editor")).toBeNull();
  await u.click(getByRole("button", { name: "status = non_compliant" }));
  expect(await findByText("editor")).toBeTruthy();
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
