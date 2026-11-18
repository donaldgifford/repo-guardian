import { SidebarInset, SidebarProvider } from "@donaldgifford/design-system/components/sidebar";
import { Outlet } from "@tanstack/react-router";
import { useState } from "react";

import { useMe } from "../../api/queries";
import { readSidebarOpen, writeSidebarOpen } from "../../lib/sidebar";
import { hasNoAccess } from "../../lib/viewmodel";
import { AccessView } from "../../views/access";
import { Loading } from "../common";
import { CrumbLabelProvider } from "../page/Breadcrumbs";
import { AppSidebar } from "./AppSidebar";
import { AppTopBar } from "./AppTopBar";
import { CommandPalette } from "./CommandPalette";

// Shell is the authenticated frame (DESIGN-0039 §Shell): the package's
// full-height collapsible sidebar beside the content, whose top bar
// carries the breadcrumbs, and the ⌘K palette. The collapsed state is
// kept in localStorage. It asks /me first, so a caller with no visible
// org sees AccessView and never a half-rendered frame.
export function Shell() {
  const me = useMe();
  const [open, setOpen] = useState(readSidebarOpen);
  const [paletteOpen, setPaletteOpen] = useState(false);
  if (!me.data) {
    return <Loading />;
  }
  if (hasNoAccess(me.data)) {
    return <AccessView me={me.data} />;
  }
  const changeOpen = (next: boolean) => {
    setOpen(next);
    writeSidebarOpen(next);
  };
  return (
    <SidebarProvider open={open} onOpenChange={changeOpen}>
      <CrumbLabelProvider>
        <AppSidebar me={me.data} />
        <SidebarInset className="min-w-0">
          <AppTopBar onSearch={() => setPaletteOpen(true)} />
          <CommandPalette open={paletteOpen} onOpenChange={setPaletteOpen} />
          <div className="px-[18px] py-4">
            <div className="mx-auto max-w-[1400px] space-y-5">
              <Outlet />
            </div>
          </div>
        </SidebarInset>
      </CrumbLabelProvider>
    </SidebarProvider>
  );
}
