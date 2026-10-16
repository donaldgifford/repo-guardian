import { Outlet } from "@tanstack/react-router";
import { useState } from "react";

import { useMe } from "../../api/queries";
import { hasNoAccess } from "../../lib/viewmodel";
import { AccessView } from "../../views/access";
import { Loading } from "../common";
import { Breadcrumbs } from "../page/Breadcrumbs";
import { CommandPalette } from "./CommandPalette";
import { MobileSidebar, Sidebar } from "./Sidebar";
import { TopBar } from "./TopBar";

// Shell is the authenticated frame (DESIGN-0036 §Shell): the top bar,
// the sidebar (a drawer below the nav breakpoint), the command palette
// and a scrolling main column that opens with the breadcrumbs. It asks
// /me first, so a caller with no visible org sees AccessView and never
// a half-rendered page.
export function Shell() {
  const me = useMe();
  const [menuOpen, setMenuOpen] = useState(false);
  const [paletteOpen, setPaletteOpen] = useState(false);
  if (!me.data) {
    return <Loading />;
  }
  if (hasNoAccess(me.data)) {
    return <AccessView me={me.data} />;
  }
  return (
    <div className="flex h-screen flex-col">
      <TopBar me={me.data} onSearch={() => setPaletteOpen(true)} onMenu={() => setMenuOpen(true)} />
      <div className="flex min-h-0 flex-1">
        <Sidebar />
        <MobileSidebar open={menuOpen} onOpenChange={setMenuOpen} />
        <main className="min-w-0 flex-1 overflow-y-auto px-[18px] py-4">
          <div className="mx-auto max-w-[1400px] space-y-5">
            <Breadcrumbs />
            <Outlet />
          </div>
        </main>
      </div>
      <CommandPalette open={paletteOpen} onOpenChange={setPaletteOpen} />
    </div>
  );
}
