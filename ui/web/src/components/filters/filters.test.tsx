// The DOM must exist before user-event loads: keep this first.
import { withDOM } from "../../../testing/dom";

import { afterEach, describe, expect, test } from "bun:test";
import { cleanup, render, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";

import { patchSearch } from "../../lib/search";
import { NOT_ENABLED } from "../GatedPanel";
import { disabledReason, FilterChips } from "./FilterChips";
import { chipsFromSearch, FILTER_KEYS, FILTER_OPTIONS, type FilterValues, patchFor, searchFromChips } from "./model";

// Roots left mounted by one case would try to remove nodes the DOM reset
// already dropped (happy-dom throws on removeChild), so unmount them
// first: hooks run in registration order.
afterEach(cleanup);
withDOM();
const user = () => userEvent.setup({ pointerEventsCheck: 0, document });

describe("filter model", () => {
  // Every documented param set at once, plus each alone: the chips are
  // a lossless view of the search, so deep links survive the bar.
  const full: FilterValues = { control: "renovate", rule: "config-present", org: "acme", decision: "baseline", pr_stale: true };

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
    expect(chipsFromSearch({ org: "acme", control: "", decision: "org_added" }).map((c) => c.key)).toEqual(["org", "decision"]);
  });

  test("patchFor types pr_stale as a boolean, clears with undefined, and a control change clears the rule", () => {
    expect(patchFor("pr_stale", "true")).toEqual({ pr_stale: true });
    expect(patchFor("pr_stale", "false")).toEqual({ pr_stale: false });
    expect(patchFor("decision", "replaced")).toEqual({ decision: "replaced" });
    expect(patchFor("decision", undefined)).toEqual({ decision: undefined });
    expect(patchFor("control", "codeowners")).toEqual({ control: "codeowners", rule: undefined });
    expect(patchFor("control", undefined)).toEqual({ control: undefined, rule: undefined });
    expect(patchSearch<Record<string, unknown>>({ control: "renovate", rule: "x", org: "acme" }, patchFor("control", "codeowners"))).toEqual({ control: "codeowners", rule: undefined, org: "acme" });
  });

  test("the closed enums list the spec's values", () => {
    expect(FILTER_OPTIONS.decision).toEqual(["baseline", "replaced", "org_added"]);
    expect(FILTER_OPTIONS.pr_stale).toEqual(["true", "false"]);
  });

  test("rule needs a control and pr_stale needs the Remediation App", () => {
    expect(disabledReason("rule", {}, true)).toBe("choose a control first");
    expect(disabledReason("rule", { control: "renovate" }, true)).toBeUndefined();
    expect(disabledReason("pr_stale", {}, false)).toBe(NOT_ENABLED);
    expect(disabledReason("pr_stale", {}, true)).toBeUndefined();
    expect(disabledReason("pr_stale", {}, undefined)).toBeUndefined();
    expect(disabledReason("org", {}, false)).toBeUndefined();
  });
});

function Harness({ initial, remediation = true }: { initial: FilterValues; remediation?: boolean }) {
  const [values, setValues] = useState<FilterValues>(initial);
  return (
    <>
      <FilterChips
        values={values}
        onChange={(patch) => setValues((v) => patchSearch(v, patch))}
        options={{ controls: ["codeowners", "renovate"], rules: values.control === "renovate" ? ["config-present", "schedule"] : [], orgs: ["acme"] }}
        remediation={remediation}
      />
      <output data-testid="search">{JSON.stringify(searchFromChips(chipsFromSearch(values)))}</output>
    </>
  );
}

describe("FilterChips", () => {
  test("a chip opens its options, picking one writes the patch, and clear removes it", async () => {
    const u = user();
    const { getByRole, findByRole, getByTestId, queryByRole } = render(<Harness initial={{ decision: "baseline", org: "acme" }} />);
    expect(getByRole("button", { name: "decision filter: baseline" })).toBeTruthy();
    expect(getByRole("button", { name: "org filter: acme" })).toBeTruthy();

    await u.click(getByRole("button", { name: "decision filter: baseline" }));
    const dialog = await findByRole("dialog", { name: "decision filter" });
    expect(dialog.querySelector("[aria-current]")?.textContent).toBe("baseline");
    await u.click(getByRole("button", { name: "org_added" }));
    await waitFor(() => expect(getByTestId("search").textContent).toBe(JSON.stringify({ org: "acme", decision: "org_added" })));

    await u.click(getByRole("button", { name: "decision filter: org_added" }));
    await u.click(await findByRole("button", { name: "Clear filter" }));
    await waitFor(() => expect(getByTestId("search").textContent).toBe(JSON.stringify({ org: "acme" })));
    expect(queryByRole("button", { name: /decision filter/ })).toBeNull();
  });

  test("the rule chip is disabled until a control is chosen, then lists that control's rules; changing the control clears it", async () => {
    const u = user();
    const { getByRole, findByRole, getByTestId } = render(<Harness initial={{}} />);
    await u.click(getByRole("button", { name: "Add filter" }));
    await findByRole("dialog", { name: "Add filter" });
    const rule = getByRole("button", { name: /^rule/ });
    expect(rule.hasAttribute("disabled")).toBe(true);
    expect(rule.textContent).toContain("choose a control first");
    await u.click(getByRole("button", { name: "control" }));
    await u.click(await findByRole("button", { name: "renovate" }));
    await waitFor(() => expect(getByTestId("search").textContent).toBe(JSON.stringify({ control: "renovate" })));

    await u.click(getByRole("button", { name: "Add filter" }));
    await u.click(await findByRole("button", { name: "rule" }));
    await u.click(await findByRole("button", { name: "schedule" }));
    await waitFor(() => expect(getByTestId("search").textContent).toBe(JSON.stringify({ control: "renovate", rule: "schedule" })));

    await u.click(getByRole("button", { name: "control filter: renovate" }));
    await u.click(await findByRole("button", { name: "codeowners" }));
    await waitFor(() => expect(getByTestId("search").textContent).toBe(JSON.stringify({ control: "codeowners" })));
  });

  test("+ filter pr_stale lands as a boolean with the App, and is disabled without it", async () => {
    const u = user();
    const on = render(<Harness initial={{}} />);
    await u.click(on.getByRole("button", { name: "Add filter" }));
    await u.click(await on.findByRole("button", { name: "pr_stale" }));
    await u.click(await on.findByRole("button", { name: "true" }));
    await waitFor(() => expect(on.getByTestId("search").textContent).toBe(JSON.stringify({ pr_stale: true })));
    expect(on.getByRole("button", { name: "pr_stale filter: true" })).toBeTruthy();
    on.unmount();

    const off = render(<Harness initial={{}} remediation={false} />);
    await u.click(off.getByRole("button", { name: "Add filter" }));
    await off.findByRole("dialog", { name: "Add filter" });
    const stale = off.getByRole("button", { name: /^pr_stale/ });
    expect(stale.hasAttribute("disabled")).toBe(true);
    expect(stale.textContent).toContain(NOT_ENABLED);
  });

  test("the org chip takes a typed value on Enter", async () => {
    const u = user();
    const { getByRole, findByRole, getByTestId } = render(<Harness initial={{}} />);
    await u.click(getByRole("button", { name: "Add filter" }));
    await u.click(await findByRole("button", { name: "org" }));
    const input = await findByRole("textbox", { name: "org value" });
    await u.type(input, "globex{Enter}");
    await waitFor(() => expect(getByTestId("search").textContent).toBe(JSON.stringify({ org: "globex" })));
  });
});
