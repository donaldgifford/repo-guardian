import { GlobalRegistrator } from "@happy-dom/global-registrator";
import { afterAll, afterEach } from "bun:test";

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

export function withDOM(): void {
  afterEach(() => {
    document.body.innerHTML = "";
  });
  afterAll(async () => {
    if (GlobalRegistrator.isRegistered) {
      await GlobalRegistrator.unregister();
    }
  });
}
