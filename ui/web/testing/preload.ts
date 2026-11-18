import { GlobalRegistrator } from "@happy-dom/global-registrator";

// Loaded once per `bun test` process, before any test file (bunfig.toml).
//
// The overlay libraries decide at module load whether a document
// exists: without one they swap useLayoutEffect for a no-op and every
// Portal stays unmounted for the rest of the process. dom.ts loads them
// under a document, but only when a DOM test file is the first to import
// them. A render-to-string test that reaches a view
// (views/controls.test.ts) imports them with no document, and which file
// runs first is the directory order of the machine: a new CI runner
// image turned twelve overlay tests red with no change to the code.
//
// So the design system's overlays (Base UI) and its palette (cmdk) are
// loaded here, through the package's own modules, under a document that
// is removed again before the first test file: the registrator also
// replaces fetch, Request and Response, and the server tests need Bun's
// own.
GlobalRegistrator.register({ url: "http://localhost/" });
await import("@donaldgifford/design-system/components/dialog");
await import("@donaldgifford/design-system/components/command");
await GlobalRegistrator.unregister();
