// The DOM must exist before radix and user-event load: keep this first.
import { withDOM } from "../../../testing/dom";

import { describe, expect, test } from "bun:test";
import { act, cleanup, render, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";

import { Command } from "./command";
import { Drawer, DrawerContent, DrawerHeader, DrawerTrigger } from "./drawer";

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
