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
