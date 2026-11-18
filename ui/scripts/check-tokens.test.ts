import { expect, test } from "bun:test";
import { mkdir, mkdtemp, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";

import { offences, scanTree } from "./check-tokens";

// The guard is only worth something if each old name trips it on its own.
test.each([
  ["a panel surface", 'className="bg-panel p-2"', "bg-panel"],
  ["a panel-2 hover", 'className="hover:bg-panel-2"', "bg-panel-2"],
  ["an ok text with opacity", 'className="text-ok/80"', "text-ok"],
  ["a warn border", "cn('border-warn')", "border-warn"],
  ["a bad fill", 'fill="x" className="fill-bad"', "fill-bad"],
  ["a panel variable", 'stroke: "var(--color-panel)"', "--color-panel"],
  ["an ok variable", "[--tone:var(--color-ok)]", "--color-ok"],
  ["a warn variable", '"var(--color-warn)"', "--color-warn"],
  ["a bad variable", '"var(--color-bad)"', "--color-bad"],
])("fails on %s", (_name, line, found) => {
  expect(offences("a.tsx", line)).toEqual([{ file: "a.tsx", line: 1, found }]);
});

test("passes the design system's names and look-alikes", () => {
  const ok = [
    'className="bg-card bg-muted text-success border-warning fill-destructive"',
    '"var(--color-card)" "var(--color-success)" "var(--color-muted-foreground)"',
    'className="bg-panelist text-okay" const ok = res.ok; tone="warning"',
  ].join("\n");
  expect(offences("a.tsx", ok)).toEqual([]);
});

test("scanTree reports file and line, and skips other files", async () => {
  const dir = await mkdtemp(join(tmpdir(), "rg-tokens-"));
  await mkdir(join(dir, "views"));
  await writeFile(join(dir, "README.md"), "bg-panel");
  await writeFile(join(dir, "views", "a.tsx"), 'const a = 1;\nconst c = "text-bad";');
  expect(await scanTree(dir)).toEqual([{ file: join(dir, "views", "a.tsx"), line: 2, found: "text-bad" }]);
});
