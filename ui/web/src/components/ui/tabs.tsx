import { useNavigate, useSearch } from "@tanstack/react-router";
import type { ReactNode } from "react";

import { patchSearch } from "../../lib/search";
import { cn } from "../../lib/utils";

export interface TabOption<V extends string> {
  value: V;
  label: ReactNode;
}

// Tabs is the segmented control (the mockup's `.seg`): one row of
// buttons, the current one lifted onto panel-2. Controlled; SearchTabs
// binds it to a search param.
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
  return (
    <div role="tablist" aria-label={label} className={cn("inline-flex overflow-hidden rounded-lg border border-border text-xs", className)}>
      {options.map((o) => {
        const selected = o.value === value;
        return (
          <button
            key={o.value}
            type="button"
            role="tab"
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
