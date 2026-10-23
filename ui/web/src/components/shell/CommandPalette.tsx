import { useNavigate } from "@tanstack/react-router";
import { useCallback, useMemo, useState } from "react";

import type { Schemas } from "../../api/client";
import { pageItems, useControls, useOrgs, useRepositories } from "../../api/queries";
import { humanize } from "../../lib/format";
import { useDebounced, useKeyboardShortcut } from "../../lib/hooks";
import { patchSearch } from "../../lib/search";
import { useOrgScope } from "../page/OrgScope";
import { Command, CommandDialog, type CommandGroup } from "../ui/command";

export interface PaletteData {
  repositories: Schemas["Repository"][];
  controls: Schemas["ControlSummary"][];
  orgs: Schemas["OrgSummary"][];
}

export interface PaletteGo {
  repository: (r: Schemas["Repository"]) => void;
  control: (c: Schemas["ControlSummary"]) => void;
  org: (o: Schemas["OrgSummary"]) => void;
}

// paletteGroups builds the three result groups from a query: the
// repositories the API found, and the controls and orgs whose names
// contain the query, from the already-loaded lists. An empty query
// lists every control and org, so the palette doubles as a jump list.
export function paletteGroups(query: string, data: PaletteData, go: PaletteGo): CommandGroup[] {
  const q = query.trim().toLowerCase();
  const matches = (...s: (string | undefined)[]) => q === "" || s.some((x) => x?.toLowerCase().includes(q));
  return [
    {
      heading: "Repositories",
      items: data.repositories.map((r) => ({
        id: `repo-${r.id}`,
        label: `${r.org}/${r.name}`,
        detail: r.active ? (r.policy_state ?? "unresolved") : `parked · ${humanize(r.park_reason ?? "unknown")}`,
        onSelect: () => go.repository(r),
      })),
    },
    {
      heading: "Controls",
      items: data.controls.filter((c) => matches(c.id, c.title)).map((c) => ({ id: `control-${c.id}`, label: c.id, detail: c.type, onSelect: () => go.control(c) })),
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

// CommandPalette is ⌘K (DESIGN-0037 OQ9): one input over grouped
// results, Enter navigates. The current ?org= scopes the repository
// search and rides along on navigation. The repository request waits
// until there is a query; controls and orgs are the lists the pages
// already hold.
export function CommandPalette({ open, onOpenChange }: { open: boolean; onOpenChange: (open: boolean) => void }) {
  const [query, setQuery] = useState("");
  const debounced = useDebounced(query, 150).trim();
  const org = useOrgScope();
  const navigate = useNavigate();
  const repositories = useRepositories({ q: debounced, ...(org ? { org } : {}) }, open && debounced !== "");
  const controls = useControls();
  const orgs = useOrgs();

  const openPalette = useCallback(() => onOpenChange(true), [onOpenChange]);
  useKeyboardShortcut("k", openPalette);

  const close = useCallback(() => {
    onOpenChange(false);
    setQuery("");
  }, [onOpenChange]);

  const repoItems = pageItems(repositories.data);
  const groups = useMemo(
    () =>
      paletteGroups(
        query,
        { repositories: debounced === "" ? [] : (repoItems ?? []), controls: controls.data?.items ?? [], orgs: orgs.data?.items ?? [] },
        {
          repository: (r) => {
            close();
            void navigate({ to: "/repositories/$id", params: { id: String(r.id) }, search: keepOrg });
          },
          control: (c) => {
            close();
            void navigate({ to: "/controls/$id", params: { id: c.id }, search: keepOrg });
          },
          org: (o) => {
            close();
            void navigate({ to: "/orgs/$org", params: { org: o.org }, search: keepOrg });
          },
        },
      ),
    [query, debounced, repoItems, controls.data, orgs.data, navigate, close],
  );

  return (
    <CommandDialog open={open} onOpenChange={(o) => (o ? onOpenChange(true) : close())} label="Search">
      <Command query={query} onQueryChange={setQuery} groups={groups} placeholder="Search repositories, controls, orgs…" empty={query.trim() === "" ? "Type to search repositories." : "No matches."} />
    </CommandDialog>
  );
}
