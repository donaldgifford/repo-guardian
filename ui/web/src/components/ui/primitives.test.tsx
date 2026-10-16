import { describe, expect, test } from "bun:test";
import { renderToStaticMarkup } from "react-dom/server";

import { Avatar } from "./avatar";
import { Button } from "./button";
import { Card } from "./card";
import { Chip } from "./chip";
import { DeltaText, Kpi } from "./kpi";
import { Meter, meterTone } from "./meter";
import { Pill, PillGroup } from "./pill";
import { StatStrip } from "./stat-strip";
import { Tabs } from "./tabs";

describe("Card", () => {
  test("with a title renders header, strip and body in order", () => {
    const html = renderToStaticMarkup(
      <Card title="Compliance" subtitle="over 30d" right={<span>R</span>} strip={<div>strip</div>}>
        body
      </Card>,
    );
    expect(html).toMatch(/<header[^>]*><div[^>]*><h3[^>]*>Compliance<\/h3><p[^>]*>over 30d<\/p><\/div><div[^>]*><span>R<\/span><\/div><\/header><div>strip<\/div><div[^>]*>body<\/div>/);
    expect(html).toContain("bg-panel");
  });

  test("without a title is the bare surface", () => {
    expect(renderToStaticMarkup(<Card>raw</Card>)).toBe('<section class="rounded-lg border border-border bg-panel">raw</section>');
  });
});

test("StatStrip renders one cell per stat with optional subline", () => {
  const html = renderToStaticMarkup(<StatStrip cells={[{ label: "Tracked", value: "412" }, { label: "Compliant", value: "87.0%", subline: "vs previous" }]} />);
  expect(html.match(/<dt/g)).toHaveLength(2);
  expect(html).toContain(">Tracked</dt>");
  expect(html).toContain(">vs previous</dd>");
});

describe("Kpi and DeltaText", () => {
  test("an upward good delta is ok, a downward one is bad", () => {
    const up = renderToStaticMarkup(<DeltaText delta={{ value: 1.2, goodUp: true, unit: " pts" }} />);
    expect(up).toContain("text-ok");
    expect(up).toContain("▲ 1.2 pts");
    const down = renderToStaticMarkup(<DeltaText delta={{ value: -3, goodUp: true }} />);
    expect(down).toContain("text-bad");
    expect(down).toContain("▼ 3");
  });

  test("a falling failing count is good; zero and null are neutral", () => {
    expect(renderToStaticMarkup(<DeltaText delta={{ value: -5, goodUp: false }} />)).toContain("text-ok");
    expect(renderToStaticMarkup(<DeltaText delta={{ value: 0, goodUp: true, unit: "%" }} />)).toBe("<span>• 0%</span>");
    expect(renderToStaticMarkup(<DeltaText delta={null} />)).toBe("<span>no previous snapshot</span>");
  });

  test("Kpi joins delta and subline", () => {
    const html = renderToStaticMarkup(<Kpi label="Failing" value="37" delta={{ value: -2, goodUp: false }} subline="vs 7d" />);
    expect(html).toContain(">Failing<");
    expect(html).toContain(">37<");
    expect(html).toMatch(/▼ 2<\/span> vs 7d/);
  });
});

test("Pill is a radio in its group with aria-checked", () => {
  const html = renderToStaticMarkup(
    <PillGroup label="Series">
      <Pill color="var(--color-ok)" label="compliant" value="87%" checked onSelect={() => {}} />
      <Pill color="var(--color-bad)" label="failing" checked={false} onSelect={() => {}} />
    </PillGroup>,
  );
  expect(html).toContain('role="radiogroup" aria-label="Series"');
  expect(html).toMatch(/role="radio" aria-checked="true" tabindex="0"/);
  expect(html).toMatch(/role="radio" aria-checked="false" tabindex="-1"/);
  expect(html).toContain(">87%</b>");
});

test("Tabs marks the selected tab", () => {
  const html = renderToStaticMarkup(
    <Tabs
      label="Range"
      options={[
        { value: "7d", label: "7d" },
        { value: "30d", label: "30d" },
      ]}
      value="30d"
      onChange={() => {}}
    />,
  );
  expect(html).toContain('role="tablist" aria-label="Range"');
  expect(html).toMatch(/aria-selected="false"[^>]*>7d</);
  expect(html).toMatch(/aria-selected="true"[^>]*>30d</);
});

test("Chip renders key = value, or the dashed add chip", () => {
  expect(renderToStaticMarkup(<Chip name="status" value="non_compliant" />)).toMatch(/status = <b[^>]*>non_compliant<\/b>/);
  const add = renderToStaticMarkup(<Chip />);
  expect(add).toContain("border-dashed");
  expect(add).toContain("+ filter");
});

describe("Meter", () => {
  test.each([
    [null, "mute"],
    [0, "bad"],
    [69.9, "bad"],
    [70, "warn"],
    [89.9, "warn"],
    [90, "ok"],
    [100, "ok"],
  ] as const)("%p → %s", (percent, tone) => {
    expect(meterTone(percent)).toBe(tone);
    expect(renderToStaticMarkup(<Meter percent={percent} />)).toContain(`data-tone="${tone}"`);
  });

  test("prints the percent and exposes it as a meter", () => {
    const html = renderToStaticMarkup(<Meter percent={87.5} />);
    expect(html).toContain('role="meter"');
    expect(html).toContain('aria-valuenow="87.5"');
    expect(html).toContain("width:87.5%");
    expect(html).toContain(">87.5%</b>");
    expect(renderToStaticMarkup(<Meter percent={null} />)).toContain('aria-valuetext="unmeasured"');
  });
});

test("Avatar is a 20px square with the initials", () => {
  const html = renderToStaticMarkup(<Avatar initials="DG" />);
  expect(html).toContain("size-5");
  expect(html).toContain("bg-avatar");
  expect(html).toContain(">DG</span>");
});

test("Button defaults to outline, type button", () => {
  expect(renderToStaticMarkup(<Button>Go</Button>)).toMatch(/<button type="button" class="[^"]*border-border[^"]*h-7/);
  expect(renderToStaticMarkup(<Button variant="ghost" size="sm">Go</Button>)).toMatch(/border-transparent[^"]*h-\[22px\]/);
});
