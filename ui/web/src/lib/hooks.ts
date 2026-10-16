import { useEffect, useState } from "react";

// useDebounced returns value once it has held still for ms.
export function useDebounced<T>(value: T, ms: number): T {
  const [settled, setSettled] = useState(value);
  useEffect(() => {
    const t = setTimeout(() => setSettled(value), ms);
    return () => clearTimeout(t);
  }, [value, ms]);
  return settled;
}

// useKeyboardShortcut calls back on ⌘<key> or Ctrl+<key> anywhere on the
// page; the handler is read through a ref-free closure each render so
// the listener never goes stale.
export function useKeyboardShortcut(key: string, onTrigger: () => void): void {
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === key && !e.altKey && !e.shiftKey) {
        e.preventDefault();
        onTrigger();
      }
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [key, onTrigger]);
}
