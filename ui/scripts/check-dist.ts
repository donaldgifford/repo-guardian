// Fails when the built SPA would break under the CSP (DESIGN-0036 § Fonts
// under the CSP): `default-src 'self'` has no data: source and no font
// host, so an inlined asset or a Google Fonts URL in ui/dist/ is a page
// that loads with missing fonts or icons. Runs `bun run build` first, then
// scans every CSS and JS file it produced. The `lint-ui` make target and
// the CI `ui` job run it after check:api.
import { $ } from "bun";
import { readdir, readFile } from "node:fs/promises";
import { join } from "node:path";

export interface Offence {
  file: string;
  pattern: string;
}

// A data: URL with a media type, in a url() or a string. Matching the
// `data:` prefix alone would flag ordinary prose and JSON keys.
const DATA_URL = /data:[a-z]+\/[a-z0-9.+-]+[;,]/i;
const GOOGLE_FONTS = /fonts\.googleapis\.com|fonts\.gstatic\.com/;

const SCANNED = /\.(css|js|mjs)$/;

export function offences(file: string, content: string): Offence[] {
  const found: Offence[] = [];
  if (DATA_URL.test(content)) found.push({ file, pattern: "data: URL" });
  if (GOOGLE_FONTS.test(content)) found.push({ file, pattern: "fonts.googleapis.com" });
  return found;
}

// scanDist walks dir and returns every offence in its CSS and JS files.
export async function scanDist(dir: string): Promise<Offence[]> {
  const entries = await readdir(dir, { recursive: true, withFileTypes: true });
  const files = entries
    .filter((e) => e.isFile() && SCANNED.test(e.name))
    .map((e) => join(e.parentPath, e.name))
    .sort();
  const results = await Promise.all(files.map(async (f) => offences(f, await readFile(f, "utf8"))));
  return results.flat();
}

if (import.meta.main) {
  await $`bun run build`.quiet();
  const found = await scanDist("dist");
  for (const o of found) {
    console.error(`${o.file}: contains a ${o.pattern}, which the CSP blocks (DESIGN-0036)`);
  }
  if (found.length > 0) process.exit(1);
  console.log("dist/ is CSP-clean");
}
