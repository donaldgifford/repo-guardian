import { TimeZoneToggle } from "@donaldgifford/design-system/blocks/time";
import { Button } from "@donaldgifford/design-system/components/button";
import { Separator } from "@donaldgifford/design-system/components/separator";
import { SidebarTrigger } from "@donaldgifford/design-system/components/sidebar";
import { Kbd, TopBarLayout } from "@donaldgifford/design-system/shell/top-bar";

import { Icon } from "../icons";
import { Breadcrumbs } from "../page/Breadcrumbs";

// AppTopBar is the bar over the content (DESIGN-0039 §Shell): the
// sidebar trigger and the breadcrumbs on the left, the Search button
// that opens the ⌘K palette and the time-zone toggle on the right.
export function AppTopBar({ onSearch }: { onSearch: () => void }) {
  return (
    <TopBarLayout
      start={
        <>
          <SidebarTrigger />
          <Separator orientation="vertical" className="mx-1 h-4" />
          <Breadcrumbs />
        </>
      }
      end={
        <>
          <Button variant="ghost" size="sm" onClick={onSearch} aria-keyshortcuts="Meta+K Control+K">
            <Icon name="search" className="size-3.5" />
            <span>Search</span>
            <Kbd>⌘K</Kbd>
          </Button>
          <TimeZoneToggle />
        </>
      }
    />
  );
}
