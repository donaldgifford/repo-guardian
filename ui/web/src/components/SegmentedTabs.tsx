import { useNavigate, useSearch } from "@tanstack/react-router";
import type { ReactNode } from "react";

import { Tabs as TabsRoot, TabsList, TabsTrigger } from "@donaldgifford/design-system/components/tabs";

import { patchSearch } from "../lib/search";

export interface TabOption<V extends string> {
  value: V;
  label: ReactNode;
}

// Tabs is the segmented control (the mockup's `.seg`) on the design
// system's tabs: a tab list with no panels. Base UI keeps only the
// selected tab in the Tab order and the list activates on focus, so
// arrows move selection and focus along the row and Home and End jump.
// Controlled; SearchTabs binds it to a search param.
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
    <TabsRoot className="contents" value={value} onValueChange={(next) => onChange(next as V)}>
      <TabsList aria-label={label} className={className}>
        {options.map((o) => (
          <TabsTrigger key={o.value} value={o.value} data-value={o.value}>
            {o.label}
          </TabsTrigger>
        ))}
      </TabsList>
    </TabsRoot>
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
