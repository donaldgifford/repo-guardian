import { GlobalRegistrator } from "@happy-dom/global-registrator";
import { afterAll, afterEach, beforeAll } from "bun:test";

// A browser DOM for one behaviour-test file (IMPL-0031 OQ3).
//
// Registration happens here, at import time, and this module must be the
// FIRST import of the test file: radix (and user-event) decide at module
// load whether a document exists — radix swaps useLayoutEffect for a
// no-op without one, and its Portal then never mounts. It is scoped to
// the file rather than a bunfig preload because the registrator also
// replaces fetch, Request and Response with happy-dom's, and the server
// tests in the same `bun test` run need Bun's own; withDOM() unregisters
// after the file. Render-to-string tests do not need any of this.
function register(): void {
  if (!GlobalRegistrator.isRegistered) {
    GlobalRegistrator.register({ url: "http://localhost/" });
  }
  // Radix positions popovers with floating-ui, which observes sizes;
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

// Load radix while this first registration is live. Radix decides at
// module load whether a document EXISTS (useLayoutEffect becomes a
// no-op without one, and every Portal then stays unmounted); it does
// not keep the document itself, so a later re-registration is fine.
// Without this, a DOM test file that imports no radix (a view with only
// cards and badges, say) unregisters in its afterAll and the next file
// loads radix into a process with no document, taking every popover and
// drawer test after it down. Found by IMPL-0031 Phase 5's status test.
// Do NOT add react-dom or testing-library here: those bind to the
// window present at load, and a second registration strands them.
await import("radix-ui");

// withDOM wires the file's hooks. It registers again in beforeAll because
// this module is evaluated once per process: a second DOM test file
// finds the import-time registration already undone by the first's
// afterAll.
export function withDOM(): void {
  beforeAll(register);
  afterEach(() => {
    document.body.innerHTML = "";
  });
  afterAll(async () => {
    if (GlobalRegistrator.isRegistered) {
      await GlobalRegistrator.unregister();
    }
  });
}
