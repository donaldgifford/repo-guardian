// The DOM must exist before the overlays and user-event load: keep this first.
import { withDOM } from "../../../testing/dom";

import { describe, expect, test } from "bun:test";
import { SidebarProvider } from "@donaldgifford/design-system/components/sidebar";
import { cleanup, render, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";

import { AppHarness, type SeededCache, testConfig, testMe } from "../../../testing/app";
import { SIDEBAR_STORAGE_KEY } from "../../lib/sidebar";
import { AppSidebar } from "./AppSidebar";
import { Shell } from "./Shell";

withDOM(cleanup);
const user = () => userEvent.setup({ pointerEventsCheck: 0, document });

const docs = { config: { ...testConfig, docs_url: "https://docs.example.com/" } };

function sidebar(harness: { initial?: string; seed?: SeededCache } = {}): ReactNode {
  return (
    <AppHarness {...harness}>
      <SidebarProvider>
        <AppSidebar me={testMe} />
      </SidebarProvider>
    </AppHarness>
  );
}

describe("AppSidebar", () => {
  test("lists the groups and marks only the current entry", async () => {
    const { findByRole, getByRole, getByText, queryByText } = render(sidebar({ initial: "/status" }));
    const nav = await findByRole("navigation", { name: "Primary" });
    expect(nav).toBeTruthy();
    expect((await findByRole("link", { name: "Status page" })).getAttribute("aria-current")).toBe("page");
    // Fleet is "/", which every path starts with: it must match exactly.
    expect(getByRole("link", { name: "Fleet" }).hasAttribute("aria-current")).toBe(false);
    expect(nav.querySelectorAll("[aria-current=page]")).toHaveLength(1);
    expect(getByText("Business")).toBeTruthy();
    expect(getByText("Public")).toBeTruthy();
    expect(queryByText("Docs")).toBeNull();
  });

  test("the header links home and the Docs group appears only with a docs_url", async () => {
    const { findByRole, getByRole, getByText } = render(sidebar({ seed: docs }));
    const link = await findByRole("link", { name: /Documentation/ });
    expect(link.getAttribute("href")).toBe("https://docs.example.com/");
    expect(link.getAttribute("rel")).toBe("noopener noreferrer");
    expect(getByText("Docs")).toBeTruthy();
    expect(getByRole("link", { name: /^Repo Guardian/ }).getAttribute("href")).toBe("/");
  });

  test("the account menu names the user and signs out through the POST form", async () => {
    const u = user();
    let submitted = 0;
    const { findByRole, getByRole, queryByRole, container } = render(sidebar({ seed: docs }));
    const trigger = await findByRole("button", { name: /^Account menu/ });
    expect(trigger.textContent).toContain("AL");
    expect(trigger.textContent).toContain("Ada Lovelace");
    expect(queryByRole("menu")).toBeNull();

    await u.click(trigger);
    const menu = await findByRole("menu");
    expect(menu.textContent).toContain("Ada Lovelace");
    expect(menu.textContent).toContain("user-1");
    expect(getByRole("menuitem", { name: /Documentation/ }).getAttribute("href")).toBe("https://docs.example.com/");

    const form = container.ownerDocument.querySelector('form[action="/auth/logout"]');
    expect(form?.getAttribute("method")).toBe("post");
    form?.addEventListener("submit", (e) => {
      e.preventDefault();
      submitted++;
    });
    await u.click(getByRole("menuitem", { name: "Sign out" }));
    await waitFor(() => expect(submitted).toBe(1));
  });
});

async function findTrigger(findAllByRole: (role: string, o: { name: string }) => Promise<HTMLElement[]>): Promise<HTMLElement> {
  const buttons = await findAllByRole("button", { name: "Toggle Sidebar" });
  const trigger = buttons.find((b) => b.dataset.slot === "sidebar-trigger");
  if (!trigger) throw new Error("no sidebar trigger");
  return trigger;
}

describe("Shell", () => {
  test("the trigger collapses the sidebar and the state is kept", async () => {
    localStorage.removeItem(SIDEBAR_STORAGE_KEY);
    const u = user();
    const { findAllByRole, container } = render(
      <AppHarness seed={{ me: testMe }}>
        <Shell />
      </AppHarness>,
    );
    // The rail is a second "Toggle Sidebar" button, out of the Tab order.
    const toggle = await findTrigger(findAllByRole);
    const state = () => container.querySelector("[data-slot=sidebar][data-state]")?.getAttribute("data-state");
    expect(state()).toBe("expanded");
    await u.click(toggle);
    await waitFor(() => expect(state()).toBe("collapsed"));
    expect(localStorage.getItem(SIDEBAR_STORAGE_KEY)).toBe("collapsed");
    await u.click(toggle);
    await waitFor(() => expect(state()).toBe("expanded"));
    expect(localStorage.getItem(SIDEBAR_STORAGE_KEY)).toBe("expanded");
  });

  test("a stored collapsed state is where it starts", async () => {
    localStorage.setItem(SIDEBAR_STORAGE_KEY, "collapsed");
    const { findAllByRole, container } = render(
      <AppHarness seed={{ me: testMe }}>
        <Shell />
      </AppHarness>,
    );
    await findTrigger(findAllByRole);
    expect(container.querySelector("[data-slot=sidebar][data-state]")?.getAttribute("data-state")).toBe("collapsed");
    localStorage.removeItem(SIDEBAR_STORAGE_KEY);
  });
});
