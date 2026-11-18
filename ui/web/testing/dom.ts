import { GlobalRegistrator } from "@happy-dom/global-registrator";
import { afterAll, afterEach, beforeAll } from "bun:test";

// A browser DOM for one behaviour-test file (IMPL-0031 OQ3).
//
// Registration happens here, at import time, and this module must be the
// FIRST import of the test file: the overlay libraries (and user-event)
// decide at module load whether a document exists — they swap
// useLayoutEffect for a no-op without one, and a Portal then never
// mounts. It is scoped to
// the file rather than a bunfig preload because the registrator also
// replaces fetch, Request and Response with happy-dom's, and the server
// tests in the same `bun test` run need Bun's own; withDOM() unregisters
// after the file. Render-to-string tests do not need any of this.
function register(): void {
  if (!GlobalRegistrator.isRegistered) {
    GlobalRegistrator.register({ url: "http://localhost/" });
  }
  // Base UI positions popovers with floating-ui, which observes sizes;
  // happy-dom lays nothing out, so a no-op observer is enough.
  if (typeof globalThis.ResizeObserver === "undefined") {
    globalThis.ResizeObserver = class {
      observe() {}
      unobserve() {}
      disconnect() {}
    } as unknown as typeof ResizeObserver;
  }
}

register();

// Load the overlay libraries while this first registration is live.
// The design system's popovers, menus, dialogs and sheets are Base UI and
// its palette is cmdk; each decides at module load whether a document
// EXISTS (useLayoutEffect becomes a no-op without one, and every Portal
// then stays unmounted), and none keeps the document itself, so a later
// re-registration is fine. Without this, a DOM test file that imports no
// overlay (a view with only cards and badges, say) unregisters in its
// afterAll and the next file loads them into a process with no
// document, taking every popover and drawer test after it down. Found
// by IMPL-0031 Phase 5's status test, with radix. They are loaded
// through the package's own modules: neither is a dependency of ours.
// Do NOT add react-dom or testing-library here: those bind to the
// window present at load, and a second registration strands them.
await import("@donaldgifford/design-system/components/dialog");
await import("@donaldgifford/design-system/components/command");

// withDOM wires the file's hooks. It registers again in beforeAll because
// this module is evaluated once per process: a second DOM test file
// finds the import-time registration already undone by the first's
// afterAll.
//
// A file that renders with testing-library passes its `cleanup`:
// `withDOM(cleanup)`. Bun exposes no global afterEach, so the library's
// automatic cleanup never registers, and a tree left mounted outlives
// its document: the next file to unmount it fails on removeChild, in
// whichever file the directory order puts next. The function is passed
// in, not imported here, because testing-library binds to the window
// present when it loads (see above).
export function withDOM(unmount?: () => void): void {
  beforeAll(register);
  afterEach(() => {
    unmount?.();
    document.body.innerHTML = "";
  });
  afterAll(async () => {
    if (GlobalRegistrator.isRegistered) {
      await GlobalRegistrator.unregister();
    }
  });
}
