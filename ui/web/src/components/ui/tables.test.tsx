// The DOM must exist before user-event loads: keep this first.
import { withDOM } from "../../../testing/dom";

import { describe, expect, test } from "bun:test";
import { render } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";

import { type Column, DataTable } from "./data-table";
import { GridChildRows, type GridColumn, type GridRow, GroupGrid, gridTotals, heatStyle } from "./group-grid";

withDOM();
const user = () => userEvent.setup({ pointerEventsCheck: 0, document });

interface Row {
  id: string;
  name: string;
  n: number;
}

const columns: Column<Row>[] = [
  { key: "name", header: "Name", cell: (r) => <a href={`/x/${r.id}`}>{r.name}</a> },
  { key: "n", header: "N", cell: (r) => r.n, numeric: true },
  { key: "age", header: "Age", cell: () => "1d", hideBelow: "compact" },
];
const rows: Row[] = [
  { id: "a", name: "alpha", n: 1 },
  { id: "b", name: "beta", n: 2 },
];

describe("DataTable", () => {
  test("rows activate on click, Enter and Space, but not through a link", async () => {
    const u = user();
    const activated: string[] = [];
    const { getAllByRole, getByRole } = render(<DataTable columns={columns} rows={rows} rowKey={(r) => r.id} onActivate={(r) => activated.push(r.id)} />);
    const [first, second] = getAllByRole("row").slice(1);
    expect(first?.getAttribute("tabindex")).toBe("0");

    await u.click(first!);
    first!.focus();
    await u.keyboard("{Enter}");
    second!.focus();
    await u.keyboard(" ");
    expect(activated).toEqual(["a", "a", "b"]);

    await u.click(getByRole("link", { name: "beta" }));
    expect(activated).toEqual(["a", "a", "b"]);
  });

  test("the selected row carries the accent tint and aria-selected; compact drops hideBelow columns", () => {
    const { getAllByRole, rerender, queryByRole } = render(<DataTable columns={columns} rows={rows} rowKey={(r) => r.id} selectedKey="b" onActivate={() => {}} />);
    const [first, second] = getAllByRole("row").slice(1);
    expect(second?.className).toContain("bg-accent/12");
    expect(second?.getAttribute("aria-selected")).toBe("true");
    expect(first?.className).not.toContain("bg-accent/12");
    expect(first?.getAttribute("aria-selected")).toBe("false");
    expect(queryByRole("columnheader", { name: "Age" })).not.toBeNull();

    rerender(<DataTable columns={columns} rows={rows} rowKey={(r) => r.id} compact onActivate={() => {}} />);
    expect(queryByRole("columnheader", { name: "Age" })).toBeNull();
  });

  test("without onActivate rows are plain and an empty list shows the empty text", () => {
    const { getAllByRole, getByText } = render(<DataTable columns={columns} rows={[]} rowKey={(r) => r.id} empty="Nothing matches." />);
    expect(getAllByRole("row")).toHaveLength(2);
    expect(getByText("Nothing matches.")).toBeTruthy();
  });
});

const gridColumns: GridColumn[] = [
  { key: "tracked", header: "Tracked", tone: "mute" },
  { key: "compliant", header: "Compliant", tone: "ok", percent: true },
  { key: "failing", header: "Failing", tone: "bad", percent: true },
];
const gridRows: GridRow[] = [
  { key: "acme", label: "acme", cells: { tracked: 10, compliant: 8, failing: 2 }, total: 10 },
  { key: "globex", label: "globex", cells: { tracked: 4, compliant: 1, failing: 3 }, total: 4 },
];

describe("GroupGrid", () => {
  test("gridTotals sums every column and the totals", () => {
    const t = gridTotals(gridRows, gridColumns);
    expect(t.cells).toEqual({ tracked: 14, compliant: 9, failing: 5 });
    expect(t.total).toBe(14);
  });

  test("heatStyle is absent for zero and clamps between 8% and 50%", () => {
    expect(heatStyle(0, 10, "ok")).toBeUndefined();
    expect(heatStyle(1, 100, "ok")?.background).toBe("color-mix(in srgb, var(--color-ok) 8%, transparent)");
    expect(heatStyle(10, 10, "bad")?.background).toBe("color-mix(in srgb, var(--color-bad) 50%, transparent)");
    expect(heatStyle(50, 10, "bad")?.background).toBe("color-mix(in srgb, var(--color-bad) 50%, transparent)");
  });

  function Harness() {
    const [open, setOpen] = useState<string[]>([]);
    return (
      <GroupGrid
        columns={gridColumns}
        rows={gridRows}
        heat={["compliant", "failing"]}
        open={open}
        onToggle={(k) => setOpen((o) => (o.includes(k) ? o.filter((x) => x !== k) : [...o, k]))}
        aria-label="orgs"
      >
        {(row) => <GridChildRows columns={gridColumns} heat={["compliant"]} rows={[{ key: `${row.key}/r`, label: "rule", cells: { tracked: 1, compliant: 1, failing: 0 }, total: 1 }]} />}
      </GroupGrid>
    );
  }

  test("expands and collapses a row, shades only heat columns, and closes with totals", async () => {
    const u = user();
    const { getByRole, container, queryByText } = render(<Harness />);
    const toggle = getByRole("button", { name: /acme/ });
    expect(toggle.getAttribute("aria-expanded")).toBe("false");
    expect(queryByText("rule")).toBeNull();

    await u.click(toggle);
    expect(toggle.getAttribute("aria-expanded")).toBe("true");
    expect(container.querySelector("[data-children=acme] [data-child='acme/r']")).not.toBeNull();

    await u.click(toggle);
    expect(queryByText("rule")).toBeNull();

    const acme = container.querySelector("tr[data-row=acme]")!;
    const heated = acme.querySelectorAll("td[data-heat]");
    expect([...heated].map((c) => c.getAttribute("data-heat"))).toEqual(["compliant", "failing"]);
    // happy-dom drops color-mix() from inline styles, so the shade is
    // asserted through the data attribute the cell also carries.
    expect([...heated].map((c) => c.getAttribute("data-shade"))).toEqual(["42", "16"]);
    expect(acme.textContent).toContain("80%");

    const total = container.querySelector("tr[data-row=__total]")!;
    expect(total.textContent).toContain("14");
    expect(total.textContent).toContain("64%");
    expect(total.querySelectorAll("td[data-heat]")).toHaveLength(0);
  });
});
