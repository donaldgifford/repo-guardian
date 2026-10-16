import { Dialog as DialogPrimitive } from "radix-ui";
import { type KeyboardEvent, type ReactNode, useEffect, useId, useMemo, useState } from "react";

import { cn } from "../../lib/utils";

export interface CommandItem {
  id: string;
  label: ReactNode;
  // detail is the mono right-hand text: an org, a kind.
  detail?: string;
  onSelect: () => void;
}

export interface CommandGroup {
  heading: string;
  items: CommandItem[];
}

// Command is the palette's body: one input over grouped results, with
// ArrowUp/ArrowDown moving an active row and Enter selecting it. The
// list is a listbox the input controls through aria-activedescendant,
// so focus never leaves the input. The caller owns the query and the
// groups; CommandDialog wraps it in a radix Dialog for ⌘K.
export function Command({
  query,
  onQueryChange,
  groups,
  placeholder = "Search…",
  empty = "No matches.",
  className,
}: {
  query: string;
  onQueryChange: (q: string) => void;
  groups: CommandGroup[];
  placeholder?: string;
  empty?: ReactNode;
  className?: string;
}) {
  const listId = useId();
  const flat = useMemo(() => groups.flatMap((g) => g.items), [groups]);
  const [active, setActive] = useState(0);
  useEffect(() => {
    setActive(0);
  }, [flat.length, query]);
  const activeItem = flat[Math.min(active, flat.length - 1)];

  const onKeyDown = (e: KeyboardEvent<HTMLInputElement>) => {
    if (e.key === "ArrowDown") {
      e.preventDefault();
      setActive((i) => (flat.length === 0 ? 0 : (i + 1) % flat.length));
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      setActive((i) => (flat.length === 0 ? 0 : (i - 1 + flat.length) % flat.length));
    } else if (e.key === "Enter" && activeItem) {
      e.preventDefault();
      activeItem.onSelect();
    }
  };

  return (
    <div className={cn("flex flex-col", className)}>
      <input
        type="text"
        role="combobox"
        aria-expanded={flat.length > 0}
        aria-controls={listId}
        aria-activedescendant={activeItem ? `${listId}-${activeItem.id}` : undefined}
        aria-autocomplete="list"
        autoFocus
        value={query}
        placeholder={placeholder}
        onChange={(e) => onQueryChange(e.target.value)}
        onKeyDown={onKeyDown}
        className="h-10 w-full border-b border-border bg-transparent px-3 text-sm outline-none placeholder:text-muted-foreground"
      />
      <div id={listId} role="listbox" className="max-h-[60vh] overflow-y-auto p-1.5">
        {flat.length === 0 ? <div className="px-2 py-3 text-xs text-muted-foreground">{empty}</div> : null}
        {groups.map((g) =>
          g.items.length === 0 ? null : (
            <div key={g.heading} role="group" aria-label={g.heading}>
              <div className="px-2 pt-2 pb-1 text-[10.5px] font-semibold tracking-[0.08em] text-muted-foreground uppercase">{g.heading}</div>
              {g.items.map((it) => {
                const selected = it === activeItem;
                return (
                  <div
                    key={it.id}
                    id={`${listId}-${it.id}`}
                    role="option"
                    aria-selected={selected}
                    onMouseEnter={() => setActive(flat.indexOf(it))}
                    onMouseDown={(e) => e.preventDefault()}
                    onClick={it.onSelect}
                    className={cn("flex cursor-default items-center justify-between gap-3 rounded-lg px-2 py-1.5 text-[13px]", selected && "bg-panel-2")}
                  >
                    <span className="min-w-0 truncate">{it.label}</span>
                    {it.detail !== undefined ? <small className="font-mono text-[11px] text-muted-foreground">{it.detail}</small> : null}
                  </div>
                );
              })}
            </div>
          ),
        )}
      </div>
    </div>
  );
}

// CommandDialog is the palette's frame: a top-anchored dialog.
export function CommandDialog({ open, onOpenChange, label, children }: { open: boolean; onOpenChange: (open: boolean) => void; label: string; children: ReactNode }) {
  return (
    <DialogPrimitive.Root open={open} onOpenChange={onOpenChange}>
      <DialogPrimitive.Portal>
        <DialogPrimitive.Overlay className="fixed inset-0 z-40 bg-black/40 data-[state=open]:animate-in data-[state=closed]:animate-out" />
        <DialogPrimitive.Content
          aria-label={label}
          className="fixed top-[12vh] left-1/2 z-50 w-[min(92vw,600px)] -translate-x-1/2 overflow-hidden rounded-lg border border-border bg-panel text-foreground shadow-[0_12px_36px_rgba(0,0,0,0.45)] outline-none data-[state=open]:animate-in data-[state=closed]:animate-out"
        >
          <DialogPrimitive.Title className="sr-only">{label}</DialogPrimitive.Title>
          {children}
        </DialogPrimitive.Content>
      </DialogPrimitive.Portal>
    </DialogPrimitive.Root>
  );
}
