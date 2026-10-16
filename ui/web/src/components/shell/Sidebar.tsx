import { Link } from "@tanstack/react-router";

import { usePolicy, useSummary, useUIConfig } from "../../api/queries";
import { cn } from "../../lib/utils";
import { Icon } from "../icons";
import { Drawer, DrawerContent, DrawerTitle } from "../ui/drawer";
import { GROUP_LABELS, navGroups, navItems } from "./nav";

// SidebarNav is the list itself: groups with small uppercase labels,
// each entry an icon, a label and an optional right-aligned mono count;
// the current item sits on panel-2 behind an accent left rule. The
// footer names the policy version and rollout state and links to
// /policy (DESIGN-0036 §Shell).
export function SidebarNav({ onNavigate }: { onNavigate?: () => void }) {
  const config = useUIConfig();
  const summary = useSummary();
  const policy = usePolicy();
  const groups = navGroups(navItems(config.data?.docs_url));
  const item = "flex items-center gap-2.5 rounded-lg border-l-2 border-transparent px-2.5 py-1.5 text-muted-foreground hover:bg-panel-2 hover:text-foreground";
  return (
    <>
      <nav aria-label="Primary" className="flex-1">
        {groups.map(([group, items]) => (
          <div key={group}>
            <div className="px-2.5 pt-3 pb-1 text-[10.5px] font-semibold tracking-[0.08em] text-muted-foreground uppercase">{GROUP_LABELS[group]}</div>
            {items.map((n) =>
              n.to !== undefined ? (
                <Link
                  key={n.label}
                  to={n.to}
                  className={item}
                  activeProps={{ className: "bg-panel-2 text-foreground border-accent", "aria-current": "page" }}
                  activeOptions={{ exact: n.to === "/" }}
                  onClick={onNavigate}
                >
                  <Icon name={n.icon} className="size-[15px] flex-none" />
                  <span>{n.label}</span>
                  {n.count && summary.data ? <em className="ml-auto font-mono text-[11px] not-italic">{n.count(summary.data).toLocaleString()}</em> : null}
                </Link>
              ) : (
                <a key={n.label} href={n.href} target="_blank" rel="noopener noreferrer" className={item} onClick={onNavigate}>
                  <Icon name={n.icon} className="size-[15px] flex-none" />
                  <span>{n.label}</span>
                  <Icon name="external" className="ml-auto size-3" />
                </a>
              ),
            )}
          </div>
        ))}
      </nav>
      <div className="mt-auto border-t border-border px-2.5 pt-3 pb-1.5 font-mono text-[11px] text-muted-foreground">
        {policy.data ? (
          <Link to="/policy" className="hover:text-foreground" onClick={onNavigate}>
            policy {policy.data.version.slice(0, 11)} · rollout {policy.data.rollout_state}
          </Link>
        ) : (
          <span>policy —</span>
        )}
      </div>
    </>
  );
}

// Sidebar is the 220px column from the nav breakpoint (900px) up.
export function Sidebar({ className }: { className?: string }) {
  return (
    <aside className={cn("hidden w-[220px] flex-none flex-col overflow-y-auto border-r border-border bg-panel px-2 py-2.5 text-[13px] nav:flex", className)}>
      <SidebarNav />
    </aside>
  );
}

// MobileSidebar is the same nav as a left drawer below the breakpoint,
// opened from the top bar's menu button.
export function MobileSidebar({ open, onOpenChange }: { open: boolean; onOpenChange: (open: boolean) => void }) {
  return (
    <Drawer open={open} onOpenChange={onOpenChange}>
      <DrawerContent side="left" className="w-[260px] px-2 py-2.5 text-[13px]" aria-describedby={undefined}>
        <DrawerTitle className="sr-only">Navigation</DrawerTitle>
        <SidebarNav onNavigate={() => onOpenChange(false)} />
      </DrawerContent>
    </Drawer>
  );
}
