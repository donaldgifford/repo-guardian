import { Dialog as DialogPrimitive } from "radix-ui";
import { type KeyboardEvent, type ReactNode, useEffect, useRef } from "react";

import { cn } from "../../lib/utils";
import { Drawer, DrawerContent, DrawerHeader } from "./drawer";

export interface DetailDrawerProps {
  open: boolean;
  onClose: () => void;
  title: ReactNode;
  subtitle?: ReactNode;
  footer?: ReactNode;
  children: ReactNode;
  // inline renders the panel in the page flow (the caller gives it a
  // grid column) instead of over a scrim.
  inline?: boolean;
  // returnFocusTo names the element to focus on close (the row that
  // opened the drawer); absent, focus returns to where it was.
  returnFocusTo?: () => HTMLElement | null | undefined;
  "aria-label": string;
  className?: string;
}

// DetailDrawer is the detail panel (DESIGN-0036 §Findings): side by
// side with the table from 1600px, an overlay drawer with a scrim
// below. Both forms share the header, body and footer; open state is
// the caller's (Findings keeps it in `?open=`).
export function DetailDrawer({ open, onClose, title, subtitle, footer, children, inline = false, returnFocusTo, "aria-label": ariaLabel, className }: DetailDrawerProps) {
  const focusBack = () => {
    const el = returnFocusTo?.();
    if (el) el.focus();
  };
  if (inline) {
    return open ? (
      <InlinePanel onClose={onClose} focusBack={focusBack} title={title} subtitle={subtitle} footer={footer} aria-label={ariaLabel} {...(className === undefined ? {} : { className })}>
        {children}
      </InlinePanel>
    ) : null;
  }
  return (
    <Drawer
      open={open}
      onOpenChange={(next) => {
        if (!next) onClose();
      }}
    >
      <DrawerContent
        // The title is the repository link; the dialog itself is named
        // the same in both forms so a test or screen reader finds one
        // "Finding" dialog however wide the window is.
        aria-label={ariaLabel}
        aria-labelledby={undefined}
        data-drawer="overlay"
        className={className}
        onCloseAutoFocus={(e) => {
          if (returnFocusTo) {
            e.preventDefault();
            focusBack();
          }
        }}
      >
        <DrawerHeader title={title} {...(subtitle === undefined ? {} : { subtitle })} />
        <div className="min-h-0 flex-1 space-y-4">{children}</div>
        {footer !== undefined ? <footer className="mt-4 flex items-center justify-between gap-2 border-t border-border pt-3">{footer}</footer> : null}
      </DrawerContent>
    </Drawer>
  );
}

function InlinePanel({ onClose, focusBack, title, subtitle, footer, children, "aria-label": ariaLabel, className }: Omit<DetailDrawerProps, "open" | "inline" | "returnFocusTo"> & { focusBack: () => void }) {
  const closeRef = useRef<HTMLButtonElement>(null);
  useEffect(() => {
    closeRef.current?.focus();
  }, [title]);
  const close = () => {
    onClose();
    // The panel unmounts on close; the row gets focus after that.
    requestAnimationFrame(focusBack);
  };
  const onKeyDown = (e: KeyboardEvent<HTMLElement>) => {
    if (e.key === "Escape") {
      e.stopPropagation();
      close();
    }
  };
  return (
    <aside
      role="dialog"
      aria-modal="false"
      aria-label={ariaLabel}
      data-drawer="inline"
      onKeyDown={onKeyDown}
      className={cn("sticky top-0 flex max-h-[calc(100vh-2rem)] flex-col self-start overflow-y-auto rounded-lg border border-border bg-panel px-[18px] py-4", className)}
    >
      <header className="mb-3.5">
        <div className="flex items-center justify-between gap-2">
          <h2 className="text-[15px] font-semibold">{title}</h2>
          <button ref={closeRef} type="button" aria-label="Close" onClick={close} className="flex size-6 flex-none items-center justify-center rounded-lg text-muted-foreground hover:bg-panel-2 hover:text-foreground">
            ×
          </button>
        </div>
        {subtitle !== undefined ? <p className="mt-[3px] font-mono text-xs text-muted-foreground">{subtitle}</p> : null}
      </header>
      <div className="min-h-0 flex-1 space-y-4">{children}</div>
      {footer !== undefined ? <footer className="mt-4 flex items-center justify-between gap-2 border-t border-border pt-3">{footer}</footer> : null}
    </aside>
  );
}

// DetailSection is a titled block inside the drawer.
export function DetailSection({ title, children, className }: { title: ReactNode; children: ReactNode; className?: string }) {
  return (
    <section className={className}>
      <h3 className="mb-1.5 text-[11px] font-semibold tracking-[0.04em] text-muted-foreground uppercase">{title}</h3>
      {children}
    </section>
  );
}

// DetailKV is the key/value block: a two-column list, keys muted.
export function DetailKV({ rows, className }: { rows: readonly { key: string; value: ReactNode }[]; className?: string }) {
  return (
    <dl className={cn("grid grid-cols-[max-content_minmax(0,1fr)] gap-x-4 gap-y-1.5 text-[13px]", className)}>
      {rows.map((r) => (
        <div key={r.key} className="contents">
          <dt className="text-muted-foreground">{r.key}</dt>
          <dd className="min-w-0">{r.value}</dd>
        </div>
      ))}
    </dl>
  );
}

// DetailInset is the recessed mono block for evidence and errors;
// everything inside is text.
export function DetailInset({ children, className, ...props }: { children: ReactNode; className?: string; "aria-label"?: string }) {
  return (
    <div className={cn("rounded-lg border border-border bg-background px-3 py-2 font-mono text-xs break-all whitespace-pre-wrap", className)} {...props}>
      {children}
    </div>
  );
}

export const DetailClose = DialogPrimitive.Close;
