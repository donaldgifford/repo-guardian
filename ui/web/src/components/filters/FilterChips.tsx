import { useState } from "react";

import { NOT_ENABLED } from "../GatedPanel";
import { Chip } from "../ui/chip";
import { Popover, PopoverContent, PopoverHeading, PopoverItem, PopoverSeparator, PopoverTrigger } from "../ui/popover";
import { chipsFromSearch, FILTER_KEYS, FILTER_OPTIONS, type FilterKey, type FilterValues, patchFor } from "./model";

export interface FilterOptions {
  controls: readonly string[];
  // rules are the chosen control's rule ids; the chip is disabled until
  // a control is chosen.
  rules: readonly string[];
  orgs: readonly string[];
}

export interface FilterChipsProps {
  values: FilterValues;
  // onChange receives a search patch (its URL-typed values or undefined
  // to clear) for the page to route through patchSearch.
  onChange: (patch: Record<string, string | boolean | undefined>) => void;
  options: FilterOptions;
  // remediation gates the Stale PR chip: undefined while /status loads,
  // false disables it with the "not enabled" reason.
  remediation: boolean | undefined;
  className?: string;
}

// disabledReason says why a filter cannot be added right now, or
// nothing when it can.
export function disabledReason(key: FilterKey, values: FilterValues, remediation: boolean | undefined): string | undefined {
  if (key === "rule" && !values.control) return "choose a control first";
  if (key === "pr_stale" && remediation === false) return NOT_ENABLED;
  return undefined;
}

// FilterChips is the findings filter bar (DESIGN-0037 §Findings): one
// `key = value` chip per set filter and a dashed "+ filter" chip. Each
// opens a popover of that filter's options; the add chip first asks
// which filter. Everything it writes is a plain search patch, so the
// URL stays the state and deep links keep working.
export function FilterChips({ values, onChange, options, remediation, className }: FilterChipsProps) {
  const chips = chipsFromSearch(values);
  const unset = FILTER_KEYS.filter((k) => !chips.some((c) => c.key === k));
  const optionsFor = (key: FilterKey): readonly string[] => {
    switch (key) {
      case "control":
        return options.controls;
      case "rule":
        return options.rules;
      case "org":
        return options.orgs;
      default:
        return FILTER_OPTIONS[key];
    }
  };
  return (
    <div role="group" aria-label="Filters" className={["flex flex-wrap items-center gap-1.5", className].filter(Boolean).join(" ")}>
      {chips.map((c) => (
        <FilterChip key={c.key} filter={c.key} value={c.value} options={optionsFor(c.key)} onChange={onChange} />
      ))}
      {unset.length > 0 ? <AddChip keys={unset} optionsFor={optionsFor} reasonFor={(k) => disabledReason(k, values, remediation)} onChange={onChange} /> : null}
    </div>
  );
}

const openValued = (filter: FilterKey) => filter === "control" || filter === "rule" || filter === "org";

// OptionList is one filter's options with the current value marked, a
// free-text row for open-valued filters, and a clear row when set.
function OptionList({ filter, value, options, onPick }: { filter: FilterKey; value: string | undefined; options: readonly string[]; onPick: (value: string | undefined) => void }) {
  const [query, setQuery] = useState("");
  const open = openValued(filter);
  const shown = query === "" ? options : options.filter((o) => o.includes(query));
  return (
    <>
      <PopoverHeading>{filter.replace("_", " ")}</PopoverHeading>
      {open ? (
        <input
          aria-label={`${filter} value`}
          className="mx-1.5 mb-1 h-7 w-[calc(100%-12px)] rounded-md border border-border bg-background px-2 font-mono text-xs"
          placeholder="type a value…"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter" && e.currentTarget.value !== "") onPick(e.currentTarget.value);
          }}
        />
      ) : null}
      <div className="max-h-72 overflow-y-auto">
        {shown.map((o) => (
          <PopoverItem key={o} current={o === value} className="font-mono text-xs" onClick={() => onPick(o)}>
            {o}
          </PopoverItem>
        ))}
        {shown.length === 0 ? <p className="px-2 py-1.5 text-xs text-muted-foreground">{open ? "Press Enter to use the typed value." : "No options."}</p> : null}
      </div>
      {value !== undefined ? (
        <>
          <PopoverSeparator />
          <PopoverItem onClick={() => onPick(undefined)}>Clear filter</PopoverItem>
        </>
      ) : null}
    </>
  );
}

function FilterChip({ filter, value, options, onChange }: { filter: FilterKey; value: string; options: readonly string[]; onChange: FilterChipsProps["onChange"] }) {
  const [open, setOpen] = useState(false);
  const pick = (next: string | undefined) => {
    setOpen(false);
    onChange(patchFor(filter, next));
  };
  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <Chip name={filter} value={value} aria-haspopup="dialog" aria-label={`${filter} filter: ${value}`} />
      </PopoverTrigger>
      <PopoverContent aria-label={`${filter} filter`}>
        <OptionList filter={filter} value={value} options={options} onPick={pick} />
      </PopoverContent>
    </Popover>
  );
}

// AddChip is "+ filter": first the unset filter names (a filter that
// cannot be added yet is disabled with its reason), then the chosen
// filter's options in the same popover.
function AddChip({
  keys,
  optionsFor,
  reasonFor,
  onChange,
}: {
  keys: readonly FilterKey[];
  optionsFor: (key: FilterKey) => readonly string[];
  reasonFor: (key: FilterKey) => string | undefined;
  onChange: FilterChipsProps["onChange"];
}) {
  const [open, setOpen] = useState(false);
  const [editing, setEditing] = useState<FilterKey | undefined>(undefined);
  const change = (next: boolean) => {
    setOpen(next);
    if (!next) setEditing(undefined);
  };
  const pick = (next: string | undefined) => {
    if (editing !== undefined && next !== undefined) onChange(patchFor(editing, next));
    change(false);
  };
  return (
    <Popover open={open} onOpenChange={change}>
      <PopoverTrigger asChild>
        <Chip aria-haspopup="dialog" aria-label="Add filter">
          + filter
        </Chip>
      </PopoverTrigger>
      <PopoverContent aria-label="Add filter">
        {editing === undefined ? (
          <>
            <PopoverHeading>Filter by</PopoverHeading>
            {keys.map((k) => {
              const reason = reasonFor(k);
              return (
                <PopoverItem key={k} className="font-mono text-xs disabled:opacity-50" disabled={reason !== undefined} title={reason} onClick={() => setEditing(k)} {...(reason === undefined ? {} : { detail: reason })}>
                  {k}
                </PopoverItem>
              );
            })}
          </>
        ) : (
          <OptionList filter={editing} value={undefined} options={optionsFor(editing)} onPick={pick} />
        )}
      </PopoverContent>
    </Popover>
  );
}
