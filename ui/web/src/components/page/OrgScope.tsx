import { useNavigate, useSearch } from "@tanstack/react-router";
import { useState } from "react";

import { useMe, useOrgs } from "../../api/queries";
import { patchSearch } from "../../lib/search";
import { orgCountLabel, visibleOrgs } from "../../lib/viewmodel";
import { Button } from "../ui/button";
import { Popover, PopoverContent, PopoverHeading, PopoverItem, PopoverTrigger } from "../ui/popover";

// useOrgScope is the org in scope from `?org=`, or undefined for every
// visible org.
export function useOrgScope(): string | undefined {
  const search = useSearch({ strict: false }) as { org?: unknown };
  return typeof search.org === "string" && search.org !== "" ? search.org : undefined;
}

// OrgScope is the page-header control that writes `?org=` (DESIGN-0037
// OQ3): a popover listing "All orgs (N)" and each visible org with its
// tracked count. The N rule is orgCountLabel's. The key is retained
// across pages by the app route's search middleware.
export function OrgScope() {
  const [open, setOpen] = useState(false);
  const me = useMe();
  const orgs = useOrgs();
  const scope = useOrgScope();
  const navigate = useNavigate();
  if (!me.data) return null;
  const all = orgCountLabel(me.data, orgs.data?.items);
  const options = visibleOrgs(me.data, orgs.data?.items);
  const tracked = new Map(orgs.data?.items.map((o) => [o.org, o.tracked]) ?? []);
  const choose = (org: string | undefined) => {
    setOpen(false);
    void navigate({ to: ".", search: (prev: Record<string, unknown>) => patchSearch(prev, { org }) });
  };
  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <Button aria-label={`Org scope: ${scope ?? all}`} aria-haspopup="dialog" className="min-w-[150px] justify-between">
          <span className={scope ? "font-mono" : undefined}>{scope ?? all}</span>
          <span className="text-muted-foreground">▾</span>
        </Button>
      </PopoverTrigger>
      <PopoverContent aria-label="Org scope">
        <PopoverHeading>Scope</PopoverHeading>
        <PopoverItem current={!scope} onClick={() => choose(undefined)}>
          {all}
        </PopoverItem>
        {options.map((o) => {
          const n = tracked.get(o);
          return (
            <PopoverItem key={o} current={scope === o} onClick={() => choose(o)} {...(n === undefined ? {} : { detail: `${n.toLocaleString()} tracked` })}>
              <span className="font-mono">{o}</span>
            </PopoverItem>
          );
        })}
      </PopoverContent>
    </Popover>
  );
}
