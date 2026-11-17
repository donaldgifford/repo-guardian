import { GlobalRegistrator } from "@happy-dom/global-registrator";

// Loaded once per `bun test` process, before any test file (bunfig.toml).
//
// Radix decides at module load whether a document exists: without one it
// swaps useLayoutEffect for a no-op and every Portal stays unmounted for
// the rest of the process. dom.ts loads radix under a document, but only
// when a DOM test file is the first to import it. A render-to-string
// test that reaches a view (views/controls.test.ts) imports radix with
// no document, and which file runs first is the directory order of the
// machine: a new CI runner image turned twelve overlay tests red with no
// change to the code.
//
// So radix is loaded here, under a document that is removed again before
// the first test file: the registrator also replaces fetch, Request and
// Response, and the server tests need Bun's own.
GlobalRegistrator.register({ url: "http://localhost/" });
await import("radix-ui");
await GlobalRegistrator.unregister();
