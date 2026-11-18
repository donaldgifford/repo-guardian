// The sidebar's collapsed state survives a reload in localStorage under
// SIDEBAR_STORAGE_KEY. Storage can be missing or refuse access (a
// private window, a locked-down browser); then the sidebar starts
// expanded and a change simply is not kept.

export const SIDEBAR_STORAGE_KEY = "rg.sidebar";

type Store = Pick<Storage, "getItem" | "setItem">;

function storage(): Store | undefined {
  try {
    return globalThis.localStorage ?? undefined;
  } catch {
    return undefined;
  }
}

// readSidebarOpen is whether the sidebar starts expanded: true unless
// the stored value says collapsed.
export function readSidebarOpen(store: Store | undefined = storage()): boolean {
  try {
    return store?.getItem(SIDEBAR_STORAGE_KEY) !== "collapsed";
  } catch {
    return true;
  }
}

// writeSidebarOpen keeps the state for the next load.
export function writeSidebarOpen(open: boolean, store: Store | undefined = storage()): void {
  try {
    store?.setItem(SIDEBAR_STORAGE_KEY, open ? "expanded" : "collapsed");
  } catch {
    // Not kept; the sidebar still works.
  }
}
