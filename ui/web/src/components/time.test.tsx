import { afterEach, describe, expect, test } from "bun:test";
import { renderToStaticMarkup } from "react-dom/server";

import { Time, TimeZoneProvider, TimeZoneToggle } from "@donaldgifford/design-system/blocks/time";

import { ZONE_STORAGE_KEY } from "../lib/zone";

const iso = "2026-03-05T23:30:00Z";

describe("Time", () => {
  test("renders the chosen zone and names the other in title", () => {
    const utc = renderToStaticMarkup(
      <TimeZoneProvider initial="utc">
        <Time iso={iso} />
      </TimeZoneProvider>,
    );
    expect(utc).toContain(">2026-03-05 23:30</time>");
    expect(utc).toMatch(/title="\d{4}-\d\d-\d\d \d\d:\d\d local"/);
  });
});

test("TimeZoneToggle marks the current zone pressed", () => {
  const html = renderToStaticMarkup(
    <TimeZoneProvider initial="utc">
      <TimeZoneToggle />
    </TimeZoneProvider>,
  );
  expect(html).toContain('aria-label="Time zone"');
  expect(html).toMatch(/aria-pressed="false"[^>]*>Local</);
  expect(html).toMatch(/aria-pressed="true"[^>]*>UTC</);
});

describe("a choice stored by the old build", () => {
  const saved = Object.getOwnPropertyDescriptor(globalThis, "localStorage");
  afterEach(() => {
    if (saved) Object.defineProperty(globalThis, "localStorage", saved);
    else delete (globalThis as { localStorage?: Storage }).localStorage;
  });

  test(`is read back from ${ZONE_STORAGE_KEY}`, () => {
    const store = new Map([[ZONE_STORAGE_KEY, "utc"]]);
    Object.defineProperty(globalThis, "localStorage", {
      configurable: true,
      value: { getItem: (k: string) => store.get(k) ?? null, setItem: (k: string, v: string) => store.set(k, v) },
    });
    const html = renderToStaticMarkup(
      <TimeZoneProvider storageKey={ZONE_STORAGE_KEY}>
        <Time iso={iso} />
      </TimeZoneProvider>,
    );
    expect(html).toContain(">2026-03-05 23:30</time>");
  });
});
