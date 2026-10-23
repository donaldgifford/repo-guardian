// The DOM must exist before user-event loads: keep this first.
import { withDOM } from "../../testing/dom";

import { expect, test } from "bun:test";
import { render } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

import { ExportButton } from "./ExportButton";

withDOM();
const user = () => userEvent.setup({ pointerEventsCheck: 0, document });

const columns = { head: ["org", "tracked"], row: (o: { org: string; tracked: number }) => [o.org, o.tracked] };

test("ExportButton downloads the rows as CSV, named for the page, filter and day", async () => {
  const saved: [string, string][] = [];
  const { findByRole } = render(
    <ExportButton page="orgs" filter={{ org: "acme" }} columns={columns} rows={[{ org: "acme", tracked: 3 }, { org: "=evil", tracked: 1 }]} download={(n, t) => saved.push([n, t])} />,
  );
  await user().click(await findByRole("button", { name: "Export orgs as CSV" }));
  expect(saved).toHaveLength(1);
  expect(saved[0]?.[0]).toMatch(/^repo-guardian-orgs-org-acme-\d{4}-\d{2}-\d{2}\.csv$/);
  expect(saved[0]?.[1]).toBe("org,tracked\r\nacme,3\r\n'=evil,1\r\n");
});

test("ExportButton is disabled until rows load and when there are none", async () => {
  const loading = render(<ExportButton page="orgs" columns={columns} rows={undefined} download={() => {}} />);
  expect((await loading.findByRole("button")).hasAttribute("disabled")).toBe(true);
  loading.unmount();
  const empty = render(<ExportButton page="orgs" columns={columns} rows={[]} download={() => {}} />);
  expect((await empty.findByRole("button")).hasAttribute("disabled")).toBe(true);
});
