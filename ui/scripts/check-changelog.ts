// The changelog gate (IMPL-0032 7.3), part of `make lint-ui`: fails when
// CHANGELOG.yaml's newest entry is not the chart's appVersion, a tag is
// outside the closed set, dates ascend, a version repeats, or the
// committed web/src/generated/changelog.json is stale.
import { readFile } from "node:fs/promises";

import { bundlePath, changelogPath, chartAppVersion, chartPath, parseChangelog, renderBundle, toBundle, validateChangelog } from "./changelog";

const [yaml, chart, committed] = await Promise.all([readFile(changelogPath, "utf8"), readFile(chartPath, "utf8"), readFile(bundlePath, "utf8").catch(() => "")]);

let entries;
try {
  entries = parseChangelog(yaml);
} catch (err) {
  console.error(`${changelogPath}: ${err instanceof Error ? err.message : String(err)}`);
  process.exit(1);
}

const errors = validateChangelog(entries, chartAppVersion(chart));
if (renderBundle(toBundle(entries)) !== committed) errors.push(`${bundlePath} is stale: run \`bun run gen:changelog\` and commit the result`);
if (errors.length > 0) {
  for (const e of errors) console.error(`${changelogPath}: ${e}`);
  process.exit(1);
}

console.log(`${changelogPath} is current (${entries.length} entries, newest ${entries[0]?.version ?? "—"})`);
