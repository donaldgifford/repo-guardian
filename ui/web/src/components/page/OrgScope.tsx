import { useNavigate, useSearch } from "@tanstack/react-router";
import { useState } from "react";

import { useMe, useOrgs } from "../../api/queries";
import { patchSearch } from "../../lib/search";
import { orgCountLabel, visibleOrgs } from "../../lib/viewmodel";
import { Button } from "../ui/button";
import { Popover, PopoverContent, PopoverHeading, PopoverItem, PopoverTrigger } from "../ui/popover";

// OrgScope is the page-header control that writes `?org=` (DESIGN-0036
// §Shell): a popover listing "All orgs (N)" and each visible org with
// its tracked count. The N rule is orgCountLabel's.
export function OrgScope() {
  const [open, setOpen] = useState(false);
  const me = useMe();
  const orgs = useOrgs();
  const search = useSearch({ strict: false }) as { org?: string };
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
        <Button aria-label="Org scope" aria-haspopup="dialog" className="min-w-[150px] justify-between">
          <span className={search.org ? "font-mono" : undefined}>{search.org ?? all}</span>
          <span className="text-muted-foreground">▾</span>
        </Button>
      </PopoverTrigger>
      <PopoverContent aria-label="Org scope">
        <PopoverHeading>Scope</PopoverHeading>
        <PopoverItem current={!search.org} onClick={() => choose(undefined)}>
          {all}
        </PopoverItem>
        {options.map((o) => {
          const n = tracked.get(o);
          return (
            <PopoverItem key={o} current={search.org === o} onClick={() => choose(o)} {...(n === undefined ? {} : { detail: `${n.toLocaleString()} tracked` })}>
              <span className="font-mono">{o}</span>
            </PopoverItem>
          );
        })}
      </PopoverContent>
    </Popover>
  );
}
