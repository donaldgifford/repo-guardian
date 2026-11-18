// Fails when web/src names a colour token the design system does not have
// (IMPL-0037 task 2.10, DESIGN-0039 §3). The local theme's panel, panel-2,
// ok, warn and bad became card, muted, success, warning and destructive;
// Tailwind silently drops a utility whose token is gone, so a stray old
// name renders uncoloured instead of failing the build. The `lint-ui`
// make target runs it.
import { readdir, readFile } from "node:fs/promises";
import { join } from "node:path";

export interface Offence {
  file: string;
  line: number;
  found: string;
}

// A utility with an old colour token (any variant prefix, any opacity),
// or a CSS variable naming one.
const UTILITY = /(?<![\w-])(?:bg|text|border|fill|stroke|ring|divide|outline|decoration|from|via|to|shadow|caret|accent)-(?:panel-2|panel|ok|warn|bad)(?![\w-])/g;
const VARIABLE = /--color-(?:panel-2|panel|ok|warn|bad)(?![\w-])/g;

const SCANNED = /\.(ts|tsx|css)$/;

export function offences(file: string, content: string): Offence[] {
  const found: Offence[] = [];
  content.split("\n").forEach((text, i) => {
    for (const re of [UTILITY, VARIABLE]) {
      for (const m of text.matchAll(re)) found.push({ file, line: i + 1, found: m[0] });
    }
  });
  return found;
}

// scanTree walks dir and returns every offence in its TS, TSX and CSS files.
export async function scanTree(dir: string): Promise<Offence[]> {
  const entries = await readdir(dir, { recursive: true, withFileTypes: true });
  const files = entries
    .filter((e) => e.isFile() && SCANNED.test(e.name))
    .map((e) => join(e.parentPath, e.name))
    .sort();
  const results = await Promise.all(files.map(async (f) => offences(f, await readFile(f, "utf8"))));
  return results.flat();
}

if (import.meta.main) {
  const found = await scanTree("web/src");
  for (const o of found) {
    console.error(`${o.file}:${o.line}: ${o.found} is a removed colour token (DESIGN-0039 §3)`);
  }
  if (found.length > 0) process.exit(1);
  console.log("web/src uses only design-system colour tokens");
}
