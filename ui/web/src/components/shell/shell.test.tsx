// The DOM must exist before radix and user-event load: keep this first.
import { withDOM } from "../../../testing/dom";

import { describe, expect, test } from "bun:test";
import { render, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

import { AppHarness, testConfig, testMe } from "../../../testing/app";
import { SidebarNav } from "./Sidebar";
import { TopBar } from "./TopBar";

withDOM();
const user = () => userEvent.setup({ pointerEventsCheck: 0, document });

describe("SidebarNav", () => {
  test("lists the groups and marks the current entry", async () => {
    const { findByRole, getByRole, queryByText, getByText } = render(
      <AppHarness initial="/status">
        <SidebarNav />
      </AppHarness>,
    );
    const status = await findByRole("link", { name: /Status page/ });
    expect(status.getAttribute("aria-current")).toBe("page");
    expect(getByRole("link", { name: /Overview/ }).getAttribute("aria-current")).toBeNull();
    expect(getByText("Business")).toBeTruthy();
    expect(getByText("Public")).toBeTruthy();
    expect(queryByText("Docs")).toBeNull();
  });

  test("shows the Docs group only with a docs_url", async () => {
    const { findByRole, getByText } = render(
      <AppHarness seed={{ config: { ...testConfig, docs_url: "https://docs.example.com/" } }}>
        <SidebarNav />
      </AppHarness>,
    );
    const docs = await findByRole("link", { name: /Documentation/ });
    expect(docs.getAttribute("href")).toBe("https://docs.example.com/");
    expect(docs.getAttribute("rel")).toBe("noopener noreferrer");
    expect(getByText("Docs")).toBeTruthy();
  });
});

describe("TopBar", () => {
  test("the avatar menu names the user and signs out through the POST form", async () => {
    const u = user();
    let submitted = 0;
    const { findByRole, getByRole, queryByRole, container } = render(
      <AppHarness seed={{ config: { ...testConfig, docs_url: "https://docs.example.com/" } }}>
        <TopBar me={testMe} onMenu={() => {}} />
      </AppHarness>,
    );
    const avatar = await findByRole("button", { name: "Account menu" });
    expect(avatar.textContent).toBe("AL");
    expect(queryByRole("menu")).toBeNull();

    await u.click(avatar);
    const menu = await findByRole("menu");
    expect(menu.textContent).toContain("Ada Lovelace");
    expect(menu.textContent).toContain("user-1");
    expect(getByRole("menuitem", { name: /Documentation/ }).getAttribute("href")).toBe("https://docs.example.com/");

    const form = container.querySelector('form[action="/auth/logout"]');
    expect(form?.getAttribute("method")).toBe("post");
    form?.addEventListener("submit", (e) => {
      e.preventDefault();
      submitted++;
    });
    await u.click(getByRole("menuitem", { name: "Sign out" }));
    await waitFor(() => expect(submitted).toBe(1));
  });

});
