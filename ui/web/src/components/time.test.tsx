import { describe, expect, test } from "bun:test";
import { renderToStaticMarkup } from "react-dom/server";

import { Time, TimeZoneProvider, TimeZoneToggle } from "./time";

const iso = "2026-03-05T23:30:00Z";

describe("Time", () => {
  test("renders the chosen zone and names the other in title", () => {
    const utc = renderToStaticMarkup(
      <TimeZoneProvider initial="utc">
        <Time iso={iso} />
      </TimeZoneProvider>,
    );
    expect(utc).toContain(">2026-03-05 23:30</time>");
    expect(utc).toContain(`dateTime="${iso}"`);
    expect(utc).toMatch(/title="\d{4}-\d\d-\d\d \d\d:\d\d LOCAL"/);

    const local = renderToStaticMarkup(
      <TimeZoneProvider initial="local">
        <Time iso={iso} />
      </TimeZoneProvider>,
    );
    expect(local).toContain('title="2026-03-05 23:30 UTC"');
  });

  test("relative style puts the absolute time in title", () => {
    const html = renderToStaticMarkup(
      <TimeZoneProvider initial="utc">
        <Time iso={iso} style="relative" />
      </TimeZoneProvider>,
    );
    expect(html).toContain('title="2026-03-05 23:30"');
  });

  test("a missing timestamp is a dash, and no provider means local", () => {
    expect(renderToStaticMarkup(<Time iso={undefined} />)).toBe('<span class="text-muted-foreground">—</span>');
    expect(renderToStaticMarkup(<Time iso={iso} />)).toContain("UTC\"");
  });
});

test("TimeZoneToggle marks the current zone checked", () => {
  const html = renderToStaticMarkup(
    <TimeZoneProvider initial="utc">
      <TimeZoneToggle />
    </TimeZoneProvider>,
  );
  expect(html).toContain('role="radiogroup"');
  expect(html).toMatch(/aria-checked="false"[^>]*>Local</);
  expect(html).toMatch(/aria-checked="true"[^>]*>UTC</);
});
