import { expect, test } from "bun:test";

import { readSidebarOpen, SIDEBAR_STORAGE_KEY, writeSidebarOpen } from "./sidebar";

function memory() {
  const m = new Map<string, string>();
  return { getItem: (k: string) => m.get(k) ?? null, setItem: (k: string, v: string) => void m.set(k, v), m };
}

test("a write is read back, under rg.sidebar", () => {
  const s = memory();
  writeSidebarOpen(false, s);
  expect(s.m.get(SIDEBAR_STORAGE_KEY)).toBe("collapsed");
  expect(readSidebarOpen(s)).toBe(false);
  writeSidebarOpen(true, s);
  expect(readSidebarOpen(s)).toBe(true);
});

test("nothing stored, an unknown value, or no storage reads expanded", () => {
  expect(readSidebarOpen(memory())).toBe(true);
  const s = memory();
  s.m.set(SIDEBAR_STORAGE_KEY, "junk");
  expect(readSidebarOpen(s)).toBe(true);
  expect(readSidebarOpen(undefined)).toBe(true);
});

test("storage that throws reads expanded and a write does not throw", () => {
  const refusing = {
    getItem: () => {
      throw new Error("SecurityError");
    },
    setItem: () => {
      throw new Error("QuotaExceededError");
    },
  };
  expect(readSidebarOpen(refusing)).toBe(true);
  expect(() => writeSidebarOpen(false, refusing)).not.toThrow();
});
