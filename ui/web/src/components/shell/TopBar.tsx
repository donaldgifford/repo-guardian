import { Link } from "@tanstack/react-router";
import { useRef } from "react";

import type { Schemas } from "../../api/client";
import { useUIConfig } from "../../api/queries";
import { initials } from "../../lib/viewmodel";
import { Icon } from "../icons";
import { TimeZoneToggle } from "../time";
import { Avatar } from "../ui/avatar";
import { Button } from "../ui/button";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuLabel, DropdownMenuSeparator, DropdownMenuTrigger } from "../ui/dropdown-menu";

// TopBar is the 40px bar over the body (DESIGN-0036 §Shell): a reserved
// slot, the brand, then search, the time-zone toggle and the avatar
// menu. Sign out stays a POST form so the BFF's Origin check holds.
export function TopBar({ me, onSearch, onMenu }: { me: Schemas["Me"]; onSearch: () => void; onMenu: () => void }) {
  const config = useUIConfig();
  const logout = useRef<HTMLFormElement>(null);
  const docsUrl = config.data?.docs_url;
  return (
    <header className="flex h-10 flex-none items-center gap-3 border-b border-border bg-panel px-3.5 text-[13px]">
      <Button variant="ghost" size="sm" aria-label="Open navigation" className="nav:hidden" onClick={onMenu}>
        <Icon name="menu" />
      </Button>
      {/* Reserved for a portal's app switcher; empty on purpose. */}
      <div data-slot="app-switcher" />
      <Link to="/" className="flex items-center gap-2 font-semibold text-foreground">
        <Icon name="shield" className="size-4 text-accent" />
        <span>Repo Guardian</span>
        <small className="font-normal text-muted-foreground">compliance · read-only</small>
      </Link>
      <span className="flex-1" />
      <Button variant="ghost" size="sm" onClick={onSearch} aria-keyshortcuts="Meta+K Control+K">
        <Icon name="search" className="size-3.5" />
        <span>Search</span>
        <kbd className="rounded-[2px] border border-border px-1 font-mono text-[10px] text-muted-foreground">⌘K</kbd>
      </Button>
      <TimeZoneToggle />
      <DropdownMenu>
        <DropdownMenuTrigger aria-label="Account menu" className="rounded-lg">
          <Avatar initials={initials(me.name, me.subject)} />
        </DropdownMenuTrigger>
        <DropdownMenuContent>
          <DropdownMenuLabel>
            <div className="text-foreground">{me.name || me.subject}</div>
            <div className="font-mono text-[11px]">{me.subject}</div>
          </DropdownMenuLabel>
          <DropdownMenuSeparator />
          {docsUrl ? (
            <DropdownMenuItem asChild>
              <a href={docsUrl} target="_blank" rel="noopener noreferrer">
                Documentation
                <Icon name="external" className="size-3" />
              </a>
            </DropdownMenuItem>
          ) : null}
          <DropdownMenuItem onSelect={() => logout.current?.requestSubmit()}>Sign out</DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
      <form ref={logout} method="post" action="/auth/logout" className="hidden" aria-hidden="true" />
    </header>
  );
}
