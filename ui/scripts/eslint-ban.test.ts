import { expect, test } from "bun:test";
import { ESLint } from "eslint";

// The markup ban is only worth something if it fires: lint a probe that
// uses every sink and require one error per sink.
test("ESLint bans every markup sink in the SPA", async () => {
  const eslint = new ESLint({ cwd: new URL("..", import.meta.url).pathname });
  const probe = `
export function Probe({ html }: { html: string }) {
  const el = document.createElement("div");
  el.innerHTML = html;
  el.outerHTML = html;
  el.insertAdjacentHTML("beforeend", html);
  const props = { dangerouslySetInnerHTML: { __html: html } };
  return <div dangerouslySetInnerHTML={{ __html: html }} {...props} />;
}
`;
  const [result] = await eslint.lintText(probe, { filePath: "web/src/probe.tsx" });
  const banned = (result?.messages ?? []).filter((m) => m.ruleId === "no-restricted-syntax");
  expect(banned).toHaveLength(5);
});

// The radix ban (IMPL-0037 7.3): the package's Base UI components are the
// only overlay stack, in the app and in its test harness alike.
test("ESLint bans radix-ui and @radix-ui/* under web/", async () => {
  const eslint = new ESLint({ cwd: new URL("..", import.meta.url).pathname });
  const probe = `
import { Dialog } from "radix-ui";
import * as Popover from "@radix-ui/react-popover";
export const used = [Dialog, Popover];
`;
  for (const filePath of ["web/src/probe.tsx", "web/testing/probe.ts"]) {
    const [result] = await eslint.lintText(probe, { filePath });
    const banned = (result?.messages ?? []).filter((m) => m.ruleId === "no-restricted-imports");
    expect(banned, filePath).toHaveLength(2);
  }
  // The server is not the SPA: the rule does not reach it.
  const [server] = await eslint.lintText(probe, { filePath: "server/probe.ts" });
  expect((server?.messages ?? []).filter((m) => m.ruleId === "no-restricted-imports")).toHaveLength(0);
});

// The zod ban (design-system#11): zod and the two package modules that
// import it stay out of the SPA, while their zod-free siblings stay open.
test("ESLint bans zod and the package modules that import it under web/", async () => {
  const eslint = new ESLint({ cwd: new URL("..", import.meta.url).pathname });
  const probe = `
import { z } from "zod";
import { problemSchema } from "@donaldgifford/design-system/lib/problem";
import { listSearchSchema } from "@donaldgifford/design-system/lib/list-search";
import { parseSort } from "@donaldgifford/design-system/lib/list-params";
import { ApiError } from "@donaldgifford/design-system/lib/errors";
export const used = [z, problemSchema, listSearchSchema, parseSort, ApiError];
`;
  const [result] = await eslint.lintText(probe, { filePath: "web/src/probe.tsx" });
  const banned = (result?.messages ?? []).filter((m) => m.ruleId === "no-restricted-imports");
  expect(banned.map((m) => m.line)).toEqual([2, 3, 4]);
});
