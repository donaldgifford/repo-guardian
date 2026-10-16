import { useNavigate, useSearch } from "@tanstack/react-router";
import { useCallback, useMemo, useState } from "react";

import type { Schemas } from "../../api/client";
import { useOrgs, useRepositorySearch, useRules } from "../../api/queries";
import { useDebounced, useKeyboardShortcut } from "../../lib/hooks";
import { patchSearch } from "../../lib/search";
import { Command, CommandDialog, type CommandGroup } from "../ui/command";

// paletteGroups builds the three result groups from a query: the
// repositories the API found, and the rules and orgs whose names
// contain the query, from the already-loaded lists. An empty query
// lists every rule and org, so the palette doubles as a jump list.
export function paletteGroups(
  query: string,
  data: { repositories: Schemas["Repository"][]; rules: Schemas["RuleCompliance"][]; orgs: Schemas["OrgSummary"][] },
  go: { repository: (r: Schemas["Repository"]) => void; rule: (r: Schemas["RuleCompliance"]) => void; org: (o: Schemas["OrgSummary"]) => void },
): CommandGroup[] {
  const q = query.trim().toLowerCase();
  const matches = (s: string) => q === "" || s.toLowerCase().includes(q);
  return [
    {
      heading: "Repositories",
      items: data.repositories.map((r) => ({ id: `repo-${r.id}`, label: `${r.org}/${r.name}`, ...(r.active ? {} : { detail: "parked" }), onSelect: () => go.repository(r) })),
    },
    {
      heading: "Rules",
      items: data.rules.filter((r) => matches(r.name)).map((r) => ({ id: `rule-${r.kind}-${r.name}`, label: r.name, detail: r.kind, onSelect: () => go.rule(r) })),
    },
    {
      heading: "Orgs",
      items: data.orgs.filter((o) => matches(o.org)).map((o) => ({ id: `org-${o.org}`, label: o.org, detail: `${o.tracked.toLocaleString()} tracked`, onSelect: () => go.org(o) })),
    },
  ];
}

// keepOrg carries only the org scope onto the destination; a view's own
// filters stay behind.
const keepOrg = (prev: Record<string, unknown>) => patchSearch({}, { org: typeof prev.org === "string" ? prev.org : undefined });

// CommandPalette is ⌘K (DESIGN-0036 §Shell): one input over grouped
// results, Enter navigates. The current ?org= scopes the repository
// search and rides along on navigation through retainSearchParams.
export function CommandPalette({ open, onOpenChange }: { open: boolean; onOpenChange: (open: boolean) => void }) {
  const [query, setQuery] = useState("");
  const debounced = useDebounced(query, 150);
  const search = useSearch({ strict: false }) as { org?: string };
  const navigate = useNavigate();
  const repositories = useRepositorySearch(debounced, search.org);
  const rules = useRules();
  const orgs = useOrgs();

  const openPalette = useCallback(() => onOpenChange(true), [onOpenChange]);
  useKeyboardShortcut("k", openPalette);

  const close = () => {
    onOpenChange(false);
    setQuery("");
  };

  const groups = useMemo(
    () =>
      paletteGroups(
        query,
        { repositories: debounced.trim() === "" ? [] : (repositories.data?.items ?? []), rules: rules.data?.items ?? [], orgs: orgs.data?.items ?? [] },
        {
          repository: (r) => {
            close();
            void navigate({ to: "/repos/$id", params: { id: String(r.id) }, search: keepOrg });
          },
          rule: (r) => {
            close();
            void navigate({ to: "/rules/$kind/$name", params: { kind: r.kind, name: r.name }, search: keepOrg });
          },
          org: (o) => {
            close();
            void navigate({ to: "/orgs/$org", params: { org: o.org }, search: keepOrg });
          },
        },
      ),
    // close is a plain closure over stable setters.
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [query, debounced, repositories.data, rules.data, orgs.data, navigate],
  );

  return (
    <CommandDialog open={open} onOpenChange={(o) => (o ? onOpenChange(true) : close())} label="Search">
      <Command query={query} onQueryChange={setQuery} groups={groups} placeholder="Search repositories, rules and orgs…" empty={query.trim() === "" ? "Type to search repositories." : "No matches."} />
    </CommandDialog>
  );
}
