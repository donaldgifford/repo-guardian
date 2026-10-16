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
