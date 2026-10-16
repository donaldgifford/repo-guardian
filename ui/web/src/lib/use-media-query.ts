import { useSyncExternalStore } from "react";

// useMediaQuery answers a CSS media query and re-renders when it flips.
// Without matchMedia (a DOM that lays nothing out) it is false, so the
// drawer defaults to its overlay form.
export function useMediaQuery(query: string): boolean {
  return useSyncExternalStore(
    (notify) => {
      if (typeof window === "undefined" || typeof window.matchMedia !== "function") return () => {};
      const mql = window.matchMedia(query);
      mql.addEventListener("change", notify);
      return () => mql.removeEventListener("change", notify);
    },
    () => (typeof window !== "undefined" && typeof window.matchMedia === "function" ? window.matchMedia(query).matches : false),
    () => false,
  );
}

// SIDE_BY_SIDE is the width from which the detail drawer sits beside
// the table instead of over it (DESIGN-0036 §Findings).
export const SIDE_BY_SIDE = "(min-width: 1600px)";
