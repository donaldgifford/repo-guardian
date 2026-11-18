import { describe, expect, test } from "bun:test";
import { renderToStaticMarkup } from "react-dom/server";

import { DeltaText } from "./DeltaText";

describe("DeltaText", () => {
  test("an upward good delta is ok, a downward one is bad", () => {
    const up = renderToStaticMarkup(<DeltaText delta={{ value: 1.2, goodUp: true, unit: " pts" }} />);
    expect(up).toContain("text-success");
    expect(up).toContain("▲ 1.2 pts");
    const down = renderToStaticMarkup(<DeltaText delta={{ value: -3, goodUp: true }} />);
    expect(down).toContain("text-destructive");
    expect(down).toContain("▼ 3");
  });

  test("a falling failing count is good; zero and null are neutral", () => {
    expect(renderToStaticMarkup(<DeltaText delta={{ value: -5, goodUp: false }} />)).toContain("text-success");
    expect(renderToStaticMarkup(<DeltaText delta={{ value: 0, goodUp: true, unit: "%" }} />)).toBe("<span>• 0%</span>");
    expect(renderToStaticMarkup(<DeltaText delta={null} />)).toBe("<span>no previous snapshot</span>");
  });
});
