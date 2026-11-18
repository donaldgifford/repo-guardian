// The DOM must exist before user-event loads: keep this first.
import { withDOM } from "../../../testing/dom";

import { describe, expect, test } from "bun:test";
import { cleanup, render } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

import { type Column, DataTable } from "./data-table";

withDOM(cleanup);
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
    expect(second?.className).toContain("bg-primary/12");
    expect(second?.getAttribute("aria-selected")).toBe("true");
    expect(first?.className).not.toContain("bg-primary/12");
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
