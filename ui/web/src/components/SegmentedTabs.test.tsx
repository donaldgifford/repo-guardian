// The DOM must exist before Base UI and user-event load: keep this first.
import { withDOM } from "../../testing/dom";

import { expect, test } from "bun:test";
import { createMemoryHistory, createRootRoute, createRoute, createRouter, RouterProvider } from "@tanstack/react-router";
import { act, cleanup, render, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";

import { SearchTabs, Tabs } from "./SegmentedTabs";

withDOM(cleanup);
const user = () => userEvent.setup({ pointerEventsCheck: 0, document });

const options = [
  { value: "1d", label: "1d" },
  { value: "7d", label: "7d" },
  { value: "30d", label: "30d" },
];

test("with no panel the tabs render, and only the selected tab is in the Tab order", async () => {
  const u = user();
  const { getByRole, getAllByRole, queryAllByRole } = render(
    <>
      <button type="button">before</button>
      <Tabs label="Range" value="7d" onChange={() => {}} options={options} />
      <button type="button">after</button>
    </>,
  );
  expect(getByRole("tablist", { name: "Range" })).toBeTruthy();
  expect(queryAllByRole("tabpanel", { hidden: true })).toHaveLength(0);
  expect(getAllByRole("tab").map((t) => t.getAttribute("tabindex"))).toEqual(["-1", "0", "-1"]);
  await act(async () => getByRole("button", { name: "before" }).focus());
  await u.tab();
  expect(document.activeElement).toBe(getByRole("tab", { name: "7d" }));
  await u.tab();
  expect(document.activeElement).toBe(getByRole("button", { name: "after" }));
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
});
