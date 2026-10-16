// The DOM must exist before user-event loads: keep this first.
import { withDOM } from "../../../testing/dom";

import { describe, expect, test } from "bun:test";
import { render, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";

import { patchSearch } from "../../lib/search";
import { FilterChips } from "./FilterChips";
import { chipsFromSearch, FILTER_KEYS, FILTER_OPTIONS, type FilterValues, patchFor, searchFromChips } from "./model";

withDOM();
const user = () => userEvent.setup({ pointerEventsCheck: 0, document });

describe("filter model", () => {
  // Every documented param set at once, plus each alone: the chips are
  // a lossless view of the search, so deep links survive the bar.
  const full: FilterValues = { status: "non_compliant", reason: "file_missing", remediation: "pr_open", kind: "file", rule: "renovate", org: "acme", pr_stale: true };

  test("search → chips → search is the identity for every documented param", () => {
    expect(searchFromChips(chipsFromSearch(full))).toEqual(full);
    for (const key of FILTER_KEYS) {
      const one: FilterValues = { [key]: full[key] };
      expect(searchFromChips(chipsFromSearch(one))).toEqual(one);
    }
    expect(searchFromChips(chipsFromSearch({ pr_stale: false }))).toEqual({ pr_stale: false });
    expect(searchFromChips(chipsFromSearch({}))).toEqual({});
  });

  test("chips follow FILTER_KEYS order and skip empty values", () => {
    expect(chipsFromSearch({ org: "acme", status: "", kind: "file" }).map((c) => c.key)).toEqual(["kind", "org"]);
  });

  test("patchFor types pr_stale as a boolean and clears with undefined", () => {
    expect(patchFor("pr_stale", "true")).toEqual({ pr_stale: true });
    expect(patchFor("pr_stale", "false")).toEqual({ pr_stale: false });
    expect(patchFor("status", "unknown")).toEqual({ status: "unknown" });
    expect(patchFor("status", undefined)).toEqual({ status: undefined });
    expect(patchSearch<Record<string, unknown>>({ status: "x", org: "acme" }, patchFor("status", undefined))).toEqual({ status: undefined, org: "acme" });
  });

  test("every closed enum lists the spec's values", () => {
    expect(FILTER_OPTIONS.status).toEqual(["compliant", "non_compliant", "not_applicable", "unknown"]);
    expect(FILTER_OPTIONS.pr_stale).toEqual(["true", "false"]);
    expect(FILTER_OPTIONS.reason).toContain("gate_closed");
  });
});

function Harness({ initial }: { initial: FilterValues }) {
  const [values, setValues] = useState<FilterValues>(initial);
  return (
    <>
      <FilterChips values={values} onChange={(patch) => setValues((v) => patchSearch(v, patch))} rules={["codeowners", "renovate"]} orgs={["acme"]} />
      <output data-testid="search">{JSON.stringify(searchFromChips(chipsFromSearch(values)))}</output>
    </>
  );
}

describe("FilterChips", () => {
  test("a chip opens its options, picking one writes the patch, and clear removes it", async () => {
    const u = user();
    const { getByRole, findByRole, getByTestId, queryByRole } = render(<Harness initial={{ status: "non_compliant", org: "acme" }} />);
    expect(getByRole("button", { name: "status filter: non_compliant" })).toBeTruthy();
    expect(getByRole("button", { name: "org filter: acme" })).toBeTruthy();

    await u.click(getByRole("button", { name: "status filter: non_compliant" }));
    const dialog = await findByRole("dialog", { name: "status filter" });
    expect(dialog.querySelector("[aria-current]")?.textContent).toBe("non_compliant");
    await u.click(getByRole("button", { name: "unknown" }));
    await waitFor(() => expect(getByTestId("search").textContent).toBe(JSON.stringify({ status: "unknown", org: "acme" })));

    await u.click(getByRole("button", { name: "status filter: unknown" }));
    await u.click(await findByRole("button", { name: "Clear filter" }));
    await waitFor(() => expect(getByTestId("search").textContent).toBe(JSON.stringify({ org: "acme" })));
    expect(queryByRole("button", { name: /status filter/ })).toBeNull();
  });

  test("+ filter asks which filter, then its options; pr_stale lands as a boolean", async () => {
    const u = user();
    const { getByRole, findByRole, getByTestId } = render(<Harness initial={{}} />);
    await u.click(getByRole("button", { name: "Add filter" }));
    await findByRole("dialog", { name: "Add filter" });
    await u.click(getByRole("button", { name: "pr_stale" }));
    await u.click(await findByRole("button", { name: "true" }));
    await waitFor(() => expect(getByTestId("search").textContent).toBe(JSON.stringify({ pr_stale: true })));
    expect(getByRole("button", { name: "pr_stale filter: true" })).toBeTruthy();
  });

  test("the rule chip takes a typed value on Enter", async () => {
    const u = user();
    const { getByRole, findByRole, getByTestId } = render(<Harness initial={{}} />);
    await u.click(getByRole("button", { name: "Add filter" }));
    await u.click(await findByRole("button", { name: "rule" }));
    const input = await findByRole("textbox", { name: "rule value" });
    await u.type(input, "custom-rule{Enter}");
    await waitFor(() => expect(getByTestId("search").textContent).toBe(JSON.stringify({ rule: "custom-rule" })));
  });
});
