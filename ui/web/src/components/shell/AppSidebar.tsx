import { DropdownMenu, DropdownMenuContent, DropdownMenuGroup, DropdownMenuItem, DropdownMenuLabel, DropdownMenuSeparator, DropdownMenuTrigger } from "@donaldgifford/design-system/components/dropdown-menu";
import { SidebarMenuBadge, SidebarMenuButton, SidebarMenuItem, useSidebar } from "@donaldgifford/design-system/components/sidebar";
import { AppMark, AvatarMark, ShellSidebar, ShellSidebarGroup, ShellSidebarIdentity } from "@donaldgifford/design-system/shell/sidebar";
import { initialsOf } from "@donaldgifford/design-system/shell/top-bar";
import { Link, useMatchRoute } from "@tanstack/react-router";
import { useRef } from "react";

import type { Schemas } from "../../api/client";
import { useOrg, useRemediationEnabled, useRemediationOverview, useSummary, useUIConfig } from "../../api/queries";
import { addPosture } from "../../lib/posture";
import { kpis } from "../../views/remediations/model";
import { Icon, icons } from "../icons";
import { useOrgScope } from "../page/OrgScope";
import { GROUP_LABELS, type NavBadge, navGroups, navItems } from "./nav";

// useFindingsCount is the Findings entry's count: the non-compliant
// assignments in scope, the same number the Findings page reports as M
// — the fleet's from /summary, or an org's baseline plus org-added
// postures when `?org=` is set.
function useFindingsCount(): number | undefined {
  const org = useOrgScope();
  const summary = useSummary();
  const scoped = useOrg(org ?? "", org !== undefined);
  return org === undefined ? summary.data?.compliance.non_compliant : scoped.data ? addPosture(scoped.data.baseline, scoped.data.org_added).non_compliant : undefined;
}

// useRemediationsCount is the Remediations entry's count: the
// outstanding remediations (reserved, open, recommended, failed) in
// scope — the fleet's from /summary, an org's counted from its listing —
// and none without the Remediation App.
function useRemediationsCount(): number | undefined {
  const org = useOrgScope();
  const enabled = useRemediationEnabled();
  const summary = useSummary();
  const overview = useRemediationOverview(org, enabled === true && org !== undefined);
  if (enabled !== true) return undefined;
  return org === undefined ? summary.data?.remediation_backlog : overview.data ? kpis(overview.data.rows, overview.data.held).outstanding : undefined;
}

function Count({ n }: { n: number | undefined }) {
  return n === undefined ? null : <SidebarMenuBadge className="font-mono">{n.toLocaleString()}</SidebarMenuBadge>;
}

function FindingsCount() {
  return <Count n={useFindingsCount()} />;
}

function RemediationsCount() {
  return <Count n={useRemediationsCount()} />;
}

// NavCount is an entry's count beside its label, or nothing while it is
// unknown. Each count is its own component so an entry asks only for
// its own data.
function NavCount({ badge }: { badge: NavBadge }) {
  return badge === "findings" ? <FindingsCount /> : <RemediationsCount />;
}

// AppSidebar is the frame's sidebar (DESIGN-0039 §Shell): the app's
// identity on top, the nav from nav.ts, and the account menu at the
// foot. Collapsed, it is an icon rail with each label as a tooltip;
// below 768px it is a sheet that closes when an entry is chosen.
export function AppSidebar({ me }: { me: Schemas["Me"] }) {
  const config = useUIConfig();
  const groups = navGroups(navItems(config.data?.docs_url));
  const matchRoute = useMatchRoute();
  const { isMobile, setOpenMobile } = useSidebar();
  const chosen = () => {
    if (isMobile) setOpenMobile(false);
  };
  return (
    <ShellSidebar
      label="Primary"
      header={
        <SidebarMenuButton size="lg" tooltip="Repo Guardian" render={<Link to="/" onClick={chosen} />}>
          <AppMark icon={icons.shield} />
          <span className="grid flex-1 text-left leading-tight">
            <span className="truncate font-medium text-sidebar-foreground">Repo Guardian</span>
            <span className="truncate text-xs text-muted-foreground">compliance · read-only</span>
          </span>
        </SidebarMenuButton>
      }
      footer={<AccountMenu me={me} docsUrl={config.data?.docs_url} />}
    >
      {groups.map(([group, items]) => (
        <ShellSidebarGroup key={group} label={GROUP_LABELS[group]}>
          {items.map((n) => {
            if (n.to === undefined) {
              return (
                <SidebarMenuItem key={n.label}>
                  <SidebarMenuButton tooltip={n.label} render={<a href={n.href} target="_blank" rel="noopener noreferrer" onClick={chosen} />}>
                    <Icon name={n.icon} />
                    <span>{n.label}</span>
                    <Icon name="external" className="ml-auto size-3" />
                  </SidebarMenuButton>
                </SidebarMenuItem>
              );
            }
            // The link marks itself aria-current when active; isActive styles it.
            const active = Boolean(matchRoute({ to: n.to, fuzzy: n.to !== "/" }));
            return (
              <SidebarMenuItem key={n.label}>
                <SidebarMenuButton isActive={active} tooltip={n.label} render={<Link to={n.to} activeOptions={{ exact: n.to === "/" }} onClick={chosen} />}>
                  <Icon name={n.icon} />
                  <span>{n.label}</span>
                </SidebarMenuButton>
                {n.badge ? <NavCount badge={n.badge} /> : null}
              </SidebarMenuItem>
            );
          })}
        </ShellSidebarGroup>
      ))}
    </ShellSidebar>
  );
}

// AccountMenu is the sidebar's foot: the user's initials, name and
// subject, opening the menu with Documentation and Sign out. Sign out
// stays a POST form so the BFF's Origin check holds.
function AccountMenu({ me, docsUrl }: { me: Schemas["Me"]; docsUrl: string | undefined }) {
  const logout = useRef<HTMLFormElement>(null);
  const { isMobile } = useSidebar();
  const name = me.name || me.subject;
  return (
    <>
      <DropdownMenu>
        {/* Named by what it shows, after a hidden "Account menu": an
            aria-label would hide the visible name from voice control. */}
        <DropdownMenuTrigger render={<SidebarMenuButton size="lg" />}>
          <span className="sr-only">Account menu: </span>
          <ShellSidebarIdentity mark={<AvatarMark initials={initialsOf(me.name, me.subject)} />} title={name} subtitle={me.subject} />
        </DropdownMenuTrigger>
        <DropdownMenuContent side={isMobile ? "bottom" : "right"} align="end" sideOffset={6} className="min-w-[220px]">
          <DropdownMenuGroup>
            <DropdownMenuLabel className="font-normal tracking-normal normal-case">
              <div className="text-foreground">{name}</div>
              <div className="font-mono text-[11px]">{me.subject}</div>
            </DropdownMenuLabel>
          </DropdownMenuGroup>
          <DropdownMenuSeparator />
          {docsUrl ? (
            <DropdownMenuItem className="justify-between" render={<a href={docsUrl} target="_blank" rel="noopener noreferrer" />}>
              Documentation
              <Icon name="external" className="size-3" />
            </DropdownMenuItem>
          ) : null}
          <DropdownMenuItem onClick={() => logout.current?.requestSubmit()}>Sign out</DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
      <form ref={logout} method="post" action="/auth/logout" className="hidden" aria-hidden="true" />
    </>
  );
}
