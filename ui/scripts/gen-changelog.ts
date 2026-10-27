// Writes web/src/generated/changelog.json from CHANGELOG.yaml (IMPL-0032
// 7.2, OQ2): Vite has no YAML loader, so the status page imports the
// committed JSON. check-changelog.ts fails on drift.
import { readFile, writeFile } from "node:fs/promises";

import { bundlePath, changelogPath, parseChangelog, renderBundle, toBundle } from "./changelog";

const entries = parseChangelog(await readFile(changelogPath, "utf8"));
await writeFile(bundlePath, renderBundle(toBundle(entries)));
console.log(`${bundlePath}: ${entries.length} entries, running ${entries[0]?.version ?? "—"}`);
