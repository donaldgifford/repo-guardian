// The DOM must exist before user-event loads: keep this first.
import { withDOM } from "../../../testing/dom";

import { describe, expect, test } from "bun:test";
import { render } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";

import { type Column, DataTable } from "./data-table";
import { GridChildRows, type GridColumn, type GridRow, GroupGrid, heatStyle, share, shareText } from "./group-grid";

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
  { key: "compliant", header: "Compliant", tone: "ok" },
  { key: "failing", header: "Non-compliant", tone: "bad" },
  { key: "prs", header: "Open PRs", tone: "info" },
];
// Each cell carries its own denominator: acme measures 10 of 12 tracked
// (two unknown), globex measures 4; the dash is a gated cell.
const gridRows: GridRow[] = [
  { key: "acme", label: "acme", cells: { tracked: 12, compliant: { n: 8, of: 10 }, failing: { n: 2, of: 10 }, prs: <span>—</span> } },
  { key: "globex", label: "globex", cells: { tracked: 4, compliant: { n: 1, of: 4 }, failing: { n: 3, of: 4 }, prs: <span>—</span> } },
];
const children: Record<string, GridRow[]> = {
  acme: [
    { key: "acme/web", label: "web", cells: { tracked: { n: 2 }, compliant: { n: 1, of: 2 }, failing: { n: 1, of: 2 } } },
    // An excluded repository measures nothing: no data, never 0% or 100%.
    { key: "acme/api", label: "api", cells: { tracked: { n: 0 }, compliant: { n: 0, of: 0 }, failing: { n: 0, of: 0 } } },
  ],
};

describe("GroupGrid", () => {
  test("share divides by the cell's own denominator and is null when nothing is measured", () => {
    expect(share({ n: 8, of: 10 })).toBe(0.8);
    // The share floors like the subtitle's percentage: 143 of 519 is
    // 27.5%, never a rounded 28%.
    expect(shareText(143, 519)).toBe("27.5%");
    expect(shareText(376, 519)).toBe("72.4%");
    expect(shareText(3, 3)).toBe("100%");
    expect(shareText(0, 3)).toBe("0%");
    expect(share({ n: 0, of: 0 })).toBeNull();
    expect(share({ n: 3, of: null })).toBeNull();
    expect(share({ n: 3 })).toBeNull();
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
        label="Org / repository"
        columns={gridColumns}
        rows={gridRows}
        heat={["compliant", "failing"]}
        open={open}
        onToggle={(k) => setOpen((o) => (o.includes(k) ? o.filter((x) => x !== k) : [...o, k]))}
        footer={{ label: "2 orgs", cells: { tracked: 16, compliant: { n: 9, of: 14 }, failing: { n: 5, of: 14 }, prs: <span>—</span> } }}
        aria-label="orgs"
      >
        {(row) => <GridChildRows key={`${row.key}/children`} parent={row.key} rows={children[row.key] ?? []} {...(row.key === "acme" ? { footer: "showing 2 of 12" } : {})} empty="No repository." />}
      </GroupGrid>
    );
  }

  test("expands into the same body with aligned cells, shades by the measured share, and reads no data when nothing is measured", async () => {
    const u = user();
    const { getByRole, container, queryByText, getAllByRole } = render(<Harness />);
    expect(getByRole("columnheader", { name: "Org / repository" })).toBeTruthy();
    const toggle = getByRole("button", { name: /acme/ });
    expect(toggle.getAttribute("aria-expanded")).toBe("false");
    expect(queryByText("web")).toBeNull();

    await u.click(toggle);
    expect(toggle.getAttribute("aria-expanded")).toBe("true");
    // One table: child rows are rows of the same body, each with every column.
    expect(getAllByRole("table")).toHaveLength(1);
    const web = container.querySelector("tr[data-child='acme/web']")!;
    expect(web.querySelectorAll("td")).toHaveLength(gridColumns.length + 1);
    expect(web.textContent).toContain("50%");
    const api = container.querySelector("tr[data-child='acme/api']")!;
    expect(api.querySelectorAll("td[data-measured=false]")).toHaveLength(2);
    expect(api.textContent).toContain("no data");
    expect(api.textContent).not.toContain("%");
    expect([...container.querySelectorAll("tr[data-children=acme]")].at(-1)?.textContent).toContain("showing 2 of 12");

    await u.click(toggle);
    expect(queryByText("web")).toBeNull();

    // The shade follows the measured denominator (10), not tracked (12).
    const acme = container.querySelector("tr[data-row=acme]")!;
    const heated = acme.querySelectorAll("td[data-heat]");
    expect([...heated].map((c) => c.getAttribute("data-heat"))).toEqual(["compliant", "failing"]);
    // happy-dom drops color-mix() from inline styles, so the shade is
    // asserted through the data attribute the cell also carries.
    expect([...heated].map((c) => c.getAttribute("data-shade"))).toEqual(["42", "16"]);
    expect(acme.textContent).toContain("80%");
    expect(acme.textContent).toContain("—");

    await u.click(getByRole("button", { name: /globex/ }));
    expect(container.querySelector("tr[data-children=globex]")?.textContent).toContain("No repository.");

    const footer = container.querySelector("tr[data-row=__footer]")!;
    expect(footer.textContent).toContain("2 orgs");
    expect(footer.textContent).toContain("16");
    expect(footer.textContent).toContain("64.2%");
    expect(footer.querySelectorAll("td[data-heat]")).toHaveLength(0);
  });
});
