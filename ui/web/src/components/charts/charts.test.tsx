import { expect, test } from "bun:test";
import { renderToStaticMarkup } from "react-dom/server";

import { DistBar } from "./DistBar";

test("DistBar draws only non-empty segments and lists all in the legend", () => {
  const html = renderToStaticMarkup(
    <DistBar
      aria-label="fleet"
      segments={[
        { label: "compliant", value: 3, color: "var(--color-success)" },
        { label: "failing", value: 0, color: "var(--color-destructive)" },
      ]}
    />,
  );
  expect(html.match(/<i title=/g)).toHaveLength(1);
  expect(html).toContain("failing 0");
});
