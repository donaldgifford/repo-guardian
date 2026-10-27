import { afterEach, beforeEach, describe, expect, test } from "bun:test";

import { formatTime, parseTime, readZone, writeZone, ZONE_STORAGE_KEY } from "./time";

const iso = "2026-03-05T23:30:00Z";

describe("formatTime", () => {
  let tz: string | undefined;
  beforeEach(() => {
    tz = process.env.TZ;
    // A zone east of UTC so the local date rolls over past midnight.
    process.env.TZ = "Asia/Tokyo";
  });
  afterEach(() => {
    if (tz === undefined) delete process.env.TZ;
    else process.env.TZ = tz;
  });

  test("utc and local differ by the zone, date style drops the time", () => {
    expect(formatTime(iso, { zone: "utc" })).toBe("2026-03-05 23:30");
    expect(formatTime(iso, { zone: "local" })).toBe("2026-03-06 08:30");
    expect(formatTime(iso, { zone: "utc", style: "date" })).toBe("2026-03-05");
    expect(formatTime(iso, { zone: "local", style: "date" })).toBe("2026-03-06");
  });

  test("relative is zone-independent and uses the given now", () => {
    const now = new Date("2026-03-06T02:30:00Z");
    expect(formatTime(iso, { zone: "utc", style: "relative", now })).toBe("3 hours ago");
    expect(formatTime(iso, { zone: "local", style: "relative", now })).toBe("3 hours ago");
  });

  test("a Z-less input is UTC, not local", () => {
    expect(formatTime("2026-03-05T23:30:00", { zone: "utc" })).toBe("2026-03-05 23:30");
    expect(parseTime("2026-03-05T23:30:00+09:00").toISOString()).toBe("2026-03-05T14:30:00.000Z");
  });

  test("missing or unparsable renders a dash, never Invalid Date", () => {
    expect(formatTime(undefined, { zone: "utc" })).toBe("—");
    expect(formatTime("soon", { zone: "local" })).toBe("—");
  });
});

describe("zone persistence", () => {
  test("reads a stored choice and falls back to local", () => {
    const store = new Map<string, string>();
    const storage = { getItem: (k: string) => store.get(k) ?? null, setItem: (k: string, v: string) => void store.set(k, v) };
    expect(readZone(storage)).toBe("local");
    writeZone("utc", storage);
    expect(store.get(ZONE_STORAGE_KEY)).toBe("utc");
    expect(readZone(storage)).toBe("utc");
    store.set(ZONE_STORAGE_KEY, "mars");
    expect(readZone(storage)).toBe("local");
  });

  test("survives a missing or throwing localStorage", () => {
    expect(readZone(undefined)).toBe("local");
    expect(() => writeZone("utc", undefined)).not.toThrow();
    const broken = {
      getItem: () => {
        throw new Error("denied");
      },
      setItem: () => {
        throw new Error("denied");
      },
    };
    expect(readZone(broken)).toBe("local");
    expect(() => writeZone("utc", broken)).not.toThrow();
  });
});
