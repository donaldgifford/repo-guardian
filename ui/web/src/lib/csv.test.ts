import { expect, test } from "bun:test";

import { csvCell, csvTable, exportFilename, tableCsv } from "./csv";

test.each([
  ["plain", "plain"],
  ["a,b", '"a,b"'],
  ['say "hi"', '"say ""hi"""'],
  ["two\nlines", '"two\nlines"'],
  ['=HYPERLINK("x")', "\"'=HYPERLINK(\"\"x\"\")\""],
  ["+1", "'+1"],
  ["-1", "'-1"],
  ["@SUM(A1)", "'@SUM(A1)"],
  [undefined, ""],
  [null, ""],
  [42, "42"],
  [false, "false"],
])("csvCell(%p)", (v, want) => {
  expect(csvCell(v)).toBe(want);
});

test("tableCsv defuses every cell and ends each line with CRLF", () => {
  expect(tableCsv(["org", "n"], [["=acme", 1], ["a,b", undefined]])).toBe("org,n\r\n'=acme,1\r\n\"a,b\",\r\n");
});

test("csvTable renders the page's data through its columns", () => {
  const columns = { head: ["org", "tracked"], row: (o: { org: string; tracked: number }) => [o.org, o.tracked] };
  expect(csvTable(columns, [{ org: "acme", tracked: 12 }, { org: "-globex", tracked: 0 }])).toBe("org,tracked\r\nacme,12\r\n'-globex,0\r\n");
});

test("exportFilename names the page, the set filters and the day", () => {
  const now = new Date("2026-10-23T15:04:05Z");
  expect(exportFilename("controls", {}, now)).toBe("repo-guardian-controls-2026-10-23.csv");
  expect(exportFilename("org controls", { org: "acme", control: undefined, q: "" }, now)).toBe("repo-guardian-org_controls-org-acme-2026-10-23.csv");
});

test("exportFilename reduces a repository-controlled value to a safe name", () => {
  const now = new Date("2026-10-23T00:00:00Z");
  expect(exportFilename("repositories", { q: "../Etc/passwd?x=1" }, now)).toBe("repo-guardian-repositories-q-.._etc_passwd_x_1-2026-10-23.csv");
});
