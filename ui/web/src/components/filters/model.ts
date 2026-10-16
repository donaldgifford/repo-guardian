// The findings filter model: the seven /findings query parameters the
// chips edit, their documented values, and the two pure maps between a
// search object and a chip list. Both maps are total over FILTER_KEYS so
// search → chips → search is the identity (pinned by model.test.ts),
// which is what keeps every pre-existing /findings?... deep link alive.

export const FILTER_KEYS = ["status", "reason", "remediation", "kind", "rule", "org", "pr_stale"] as const;
export type FilterKey = (typeof FILTER_KEYS)[number];

export interface FilterValues {
  status?: string;
  reason?: string;
  remediation?: string;
  kind?: string;
  rule?: string;
  org?: string;
  pr_stale?: boolean;
}

export interface FilterChipValue {
  key: FilterKey;
  // value is the URL form: pr_stale carries "true" | "false".
  value: string;
}

// FILTER_OPTIONS are the values the spec documents for each closed
// enum; `rule` and `org` come from the cached lists at render time.
export const FILTER_OPTIONS: Record<Exclude<FilterKey, "rule" | "org">, readonly string[]> = {
  status: ["compliant", "non_compliant", "not_applicable", "unknown"],
  reason: [
    "file_missing",
    "assertion_failed",
    "content_differs",
    "forbidden_present",
    "setting_mismatch",
    "ruleset_missing",
    "ruleset_mismatch",
    "out_of_scope_policy",
    "out_of_scope_rule",
    "ignored_global",
    "ignored_rule",
    "gate_closed",
    "branch_missing",
    "empty_repository",
    "gate_error",
    "foreign_pr_open",
    "migrated_from_v1",
  ],
  remediation: ["none", "pr_open", "foreign_pr", "applied", "dry_run", "disabled"],
  kind: ["file", "setting", "branch_protection"],
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
// one filter in its URL type.
export function patchFor(key: FilterKey, value: string | undefined): Record<string, string | boolean | undefined> {
  if (value === undefined || value === "") return { [key]: undefined };
  return { [key]: key === "pr_stale" ? value === "true" : value };
}
