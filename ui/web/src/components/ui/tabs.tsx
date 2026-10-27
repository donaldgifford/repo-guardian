import { useNavigate, useSearch } from "@tanstack/react-router";
import type { KeyboardEvent, ReactNode } from "react";

import { patchSearch } from "../../lib/search";
import { cn } from "../../lib/utils";

export interface TabOption<V extends string> {
  value: V;
  label: ReactNode;
}

// nextTab is the WAI-ARIA tabs keyboard pattern: arrows step with
// wrap-around, Home and End jump; anything else is not ours.
export function nextTab<V extends string>(options: readonly TabOption<V>[], value: V, key: string): V | undefined {
  const i = options.findIndex((o) => o.value === value);
  if (i < 0 || options.length === 0) return undefined;
  switch (key) {
    case "ArrowRight":
    case "ArrowDown":
      return options[(i + 1) % options.length]?.value;
    case "ArrowLeft":
    case "ArrowUp":
      return options[(i - 1 + options.length) % options.length]?.value;
    case "Home":
      return options[0]?.value;
    case "End":
      return options[options.length - 1]?.value;
    default:
      return undefined;
  }
}

// Tabs is the segmented control (the mockup's `.seg`): one row of
// buttons, the current one lifted onto panel-2. Only the selected tab
// is in the Tab order; arrows move selection and focus along the row
// (automatic activation), so the control is one Tab stop. Controlled;
// SearchTabs binds it to a search param.
export function Tabs<V extends string>({
  label,
  options,
  value,
  onChange,
  className,
}: {
  label: string;
  options: readonly TabOption<V>[];
  value: V;
  onChange: (next: V) => void;
  className?: string;
}) {
  const onKeyDown = (e: KeyboardEvent<HTMLDivElement>) => {
    const next = nextTab(options, value, e.key);
    if (next === undefined || next === value) return;
    e.preventDefault();
    onChange(next);
    const el = e.currentTarget.querySelector<HTMLButtonElement>(`[data-value="${next}"]`);
    el?.focus();
  };
  return (
    <div role="tablist" aria-label={label} onKeyDown={onKeyDown} className={cn("inline-flex overflow-hidden rounded-lg border border-border text-xs", className)}>
      {options.map((o) => {
        const selected = o.value === value;
        return (
          <button
            key={o.value}
            type="button"
            role="tab"
            data-value={o.value}
            aria-selected={selected}
            tabIndex={selected ? 0 : -1}
            onClick={() => onChange(o.value)}
            className={cn("px-2.5 py-[5px]", selected ? "bg-panel-2 text-foreground" : "text-muted-foreground hover:text-foreground")}
          >
            {o.label}
          </button>
        );
      })}
    </div>
  );
}

// SearchTabs mirrors the selected tab to `?<param>=`, dropping the key
// when the default is chosen so the URL stays short. A value in the URL
// the options do not know falls back to the default rather than
// rendering no tab selected.
export function SearchTabs<V extends string>({
  param,
  defaultValue,
  options,
  ...rest
}: {
  param: string;
  defaultValue: V;
  options: readonly TabOption<V>[];
  label: string;
  className?: string;
}) {
  const search = useSearch({ strict: false }) as Record<string, unknown>;
  const navigate = useNavigate();
  const raw = search[param];
  const value = options.some((o) => o.value === raw) ? (raw as V) : defaultValue;
  return (
    <Tabs
      {...rest}
      options={options}
      value={value}
      onChange={(next) => void navigate({ to: ".", search: (prev: Record<string, unknown>) => patchSearch(prev, { [param]: next === defaultValue ? undefined : next }) })}
    />
  );
}
