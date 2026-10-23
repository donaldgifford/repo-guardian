// The findings filter model (DESIGN-0037 §Findings): the five /results
// query parameters the chips edit, and the pure maps between a search
// object and a chip list. Both maps are total over FILTER_KEYS so
// search → chips → search is the identity (pinned by filters.test.tsx),
// which is what keeps every /findings?... deep link alive.

export const FILTER_KEYS = ["control", "rule", "org", "decision", "pr_stale"] as const;
export type FilterKey = (typeof FILTER_KEYS)[number];

export interface FilterValues {
  control?: string;
  rule?: string;
  org?: string;
  decision?: string;
  pr_stale?: boolean;
}

export interface FilterChipValue {
  key: FilterKey;
  // value is the URL form: pr_stale carries "true" | "false".
  value: string;
}

// FILTER_OPTIONS are the values the spec documents for the closed
// enums; control, rule and org come from the loaded lists at render
// time. An excluded assignment has no result, so it is never a finding.
export const FILTER_OPTIONS: Record<Extract<FilterKey, "decision" | "pr_stale">, readonly string[]> = {
  decision: ["baseline", "replaced", "org_added"],
  pr_stale: ["true", "false"],
};

// chipsFromSearch lists the set filters in FILTER_KEYS order.
export function chipsFromSearch(s: FilterValues): FilterChipValue[] {
  const out: FilterChipValue[] = [];
  for (const key of FILTER_KEYS) {
    const v = s[key];
    if (v === undefined || v === "") continue;
    out.push({ key, value: String(v) });
  }
  return out;
}

// searchFromChips is the inverse: the last chip for a key wins.
export function searchFromChips(chips: readonly FilterChipValue[]): FilterValues {
  const out: FilterValues = {};
  for (const c of chips) {
    if (c.key === "pr_stale") {
      if (c.value === "true" || c.value === "false") out.pr_stale = c.value === "true";
      continue;
    }
    if (c.value !== "") out[c.key] = c.value;
  }
  return out;
}

// patchFor is the search patch that sets (or, with undefined, clears)
// one filter in its URL type. A rule belongs to a control, so changing
// or clearing the control clears the rule with it.
export function patchFor(key: FilterKey, value: string | undefined): Record<string, string | boolean | undefined> {
  const v = value === undefined || value === "" ? undefined : key === "pr_stale" ? value === "true" : value;
  if (key === "control") return { control: v, rule: undefined };
  return { [key]: v };
}
