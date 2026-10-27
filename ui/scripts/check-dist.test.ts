import { expect, test } from "bun:test";
import { mkdir, mkdtemp, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";

import { offences, scanDist } from "./check-dist";

// The guard is only worth something if each pattern trips it on its own.
test.each([
  ["a CSS data: font", "a.css", '@font-face{src:url(data:font/woff2;base64,AAAA) format("woff2")}', "data: URL"],
  ["a JS data: image", "b.js", 'const i = "data:image/svg+xml,%3Csvg%3E";', "data: URL"],
  ["a Google Fonts import", "c.css", '@import url("https://fonts.googleapis.com/css2?family=IBM+Plex+Sans");', "fonts.googleapis.com"],
  ["a gstatic font URL", "d.css", "src:url(https://fonts.gstatic.com/s/x.woff2)", "fonts.googleapis.com"],
])("fails on %s", (_name, file, content, pattern) => {
  expect(offences(file, content)).toEqual([{ file, pattern }]);
});

test("passes hashed asset references and ordinary prose", () => {
  const css = '@font-face{src:url(/assets/ibm-plex-sans-latin-400-normal-CDDApCn2.woff2) format("woff2")}';
  const js = 'const label = "metadata: none"; const key = { data: 1 };';
  expect(offences("a.css", css)).toEqual([]);
  expect(offences("b.js", js)).toEqual([]);
});

test("scanDist walks nested CSS and JS and skips other files", async () => {
  const dir = await mkdtemp(join(tmpdir(), "rg-dist-"));
  await mkdir(join(dir, "assets"));
  await writeFile(join(dir, "index.html"), '<link href="https://fonts.googleapis.com">');
  await writeFile(join(dir, "assets", "ok.css"), "body{color:red}");
  await writeFile(join(dir, "assets", "bad.js"), 'u="data:image/png;base64,AA"');
  await writeFile(join(dir, "assets", "font.woff2"), "data:font/woff2;base64,");

  const found = await scanDist(dir);
  expect(found).toEqual([{ file: join(dir, "assets", "bad.js"), pattern: "data: URL" }]);
});
