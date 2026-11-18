// The DOM must exist before user-event loads: keep this first.
import { withDOM } from "../../testing/dom";

import { describe, expect, test } from "bun:test";
import { DataTable, type DataTableColumn } from "@donaldgifford/design-system/blocks/data-table";
import { createMemoryHistory, createRootRoute, createRouter, RouterProvider } from "@tanstack/react-router";
import { cleanup, render } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";

import type { Schemas } from "../api/client";
import { controlColumns } from "../views/fleet";

withDOM(cleanup);
const user = () => userEvent.setup({ pointerEventsCheck: 0, document });

// Rendered under a router, since view columns render router links.
function withRouter(node: ReactNode) {
  const router = createRouter({ routeTree: createRootRoute({ component: () => node }), history: createMemoryHistory({ initialEntries: ["/"] }) });
  return render(<RouterProvider router={router} />);
}

const posture = { compliant: 1, non_compliant: 1, pending: 0, stale: 0, unmeasurable_access_denied: 0, unmeasurable_suspended: 0, unmeasurable_conflict: 0, not_applicable: 0, unknown: 0, excluded: 0, assigned: 2, percent: 50, coverage: 100 };
const controls = [{ id: "renovate", type: "file", compliance: posture }] as unknown as Schemas["ControlSummary"][];

describe("an embedded table", () => {
  test("with sortable off has plain-text headers: no button, no aria-sort", async () => {
    const { findByRole, getAllByRole } = withRouter(<DataTable sortable={false} bare label="Controls" columns={controlColumns} data={controls} getRowId={(c) => c.id} empty="No control is assigned yet." />);
    const table = await findByRole("table", { name: "Controls" });
    const heads = getAllByRole("columnheader");
    expect(heads.map((h) => h.textContent)).toEqual(["Control", "Type", "Compliance", "Coverage"]);
    for (const h of heads) {
      expect(h.querySelector("button")).toBeNull();
      expect(h.hasAttribute("aria-sort")).toBe(false);
    }
    expect(table.querySelectorAll("tbody tr")).toHaveLength(1);
  });

  // The view columns are display columns, which never sort; sortable
  // off is what keeps a header plain once a column has an accessor.
  test("sortable off also drops the sort button from an accessor column", () => {
    const byId: DataTableColumn<{ id: string }>[] = [{ accessorKey: "id", header: "Id" }];
    const data = [{ id: "b" }, { id: "a" }];
    const sorting = render(<DataTable bare label="Sorting" columns={byId} data={data} />);
    expect(sorting.getByRole("columnheader", { name: "Id" }).querySelector("button")).not.toBeNull();
    sorting.unmount();
    const plain = render(<DataTable sortable={false} bare label="Plain" columns={byId} data={data} />);
    expect(plain.getByRole("columnheader", { name: "Id" }).querySelector("button")).toBeNull();
  });

  test("keeps its empty text", async () => {
    const { findByText } = withRouter(<DataTable sortable={false} bare label="Controls" columns={controlColumns} data={[]} getRowId={(c) => c.id} empty="No control is assigned yet." />);
    expect(await findByText("No control is assigned yet.")).toBeTruthy();
  });
});

interface Row {
  id: string;
  name: string;
}
const columns: DataTableColumn<Row>[] = [
  { id: "name", header: "Name", cell: ({ row: { original: r } }) => <a href={`/x/${r.id}`}>{r.name}</a> },
  { id: "pick", header: "Pick", cell: () => <input aria-label="note" /> },
  { id: "age", header: "Age", cell: () => "1d", meta: { hideBelow: "compact" } },
];
const rows: Row[] = [
  { id: "a", name: "alpha" },
  { id: "b", name: "beta" },
];

describe("a list", () => {
  test("rows activate on click, Enter and Space, but not through a link or a key inside the row", async () => {
    const u = user();
    const activated: string[] = [];
    const { getAllByRole, getByRole } = render(<DataTable manual bare label="List" columns={columns} data={rows} getRowId={(r) => r.id} onRowClick={(r) => activated.push(r.id)} />);
    const [first, second] = getAllByRole("row").slice(1);
    expect(first?.getAttribute("tabindex")).toBe("0");
    expect(first?.getAttribute("data-row-id")).toBe("a");

    await u.click(first!);
    first!.focus();
    await u.keyboard("{Enter}");
    second!.focus();
    await u.keyboard(" ");
    expect(activated).toEqual(["a", "a", "b"]);

    await u.click(getByRole("link", { name: "beta" }));
    getAllByRole("textbox")[0]!.focus();
    await u.keyboard("{Enter} ");
    expect(activated).toEqual(["a", "a", "b"]);
  });

  test("marks the selected row, and compact drops the hideBelow compact columns", () => {
    const { getAllByRole, queryByRole, rerender } = render(<DataTable manual bare label="List" columns={columns} data={rows} getRowId={(r) => r.id} selectedId="b" onRowClick={() => {}} />);
    const [first, second] = getAllByRole("row").slice(1);
    expect(second?.getAttribute("aria-selected")).toBe("true");
    expect(second?.getAttribute("data-state")).toBe("selected");
    expect(first?.hasAttribute("aria-selected")).toBe(false);
    expect(queryByRole("columnheader", { name: "Age" })).not.toBeNull();

    rerender(<DataTable manual bare label="List" columns={columns} data={rows} getRowId={(r) => r.id} compact onRowClick={() => {}} />);
    expect(queryByRole("columnheader", { name: "Age" })).toBeNull();
  });

  test("shows no pager, row count or sort control", () => {
    const { container, getAllByRole } = render(<DataTable manual bare label="List" columns={columns} data={rows} getRowId={(r) => r.id} onRowClick={() => {}} />);
    expect(container.querySelector('[data-testid="row-count"], [data-testid="page-label"]')).toBeNull();
    for (const h of getAllByRole("columnheader")) expect(h.querySelector("button")).toBeNull();
  });
});
