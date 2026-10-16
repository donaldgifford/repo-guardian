import { createContext, type ReactNode, useCallback, useContext, useMemo, useState } from "react";

import { formatTime, otherZone, readZone, type TimeStyle, writeZone, type Zone } from "../lib/time";
import { cn } from "../lib/utils";

interface TimeZoneContextValue {
  zone: Zone;
  setZone: (zone: Zone) => void;
}

const TimeZoneContext = createContext<TimeZoneContextValue>({ zone: "local", setZone: () => {} });

// TimeZoneProvider holds the Local/UTC choice for the whole app and
// persists it under localStorage["rg.tz"]; without a provider every
// <Time> renders local.
export function TimeZoneProvider({ children, initial }: { children: ReactNode; initial?: Zone }) {
  const [zone, setZoneState] = useState<Zone>(() => initial ?? readZone());
  const setZone = useCallback((next: Zone) => {
    setZoneState(next);
    writeZone(next);
  }, []);
  const value = useMemo(() => ({ zone, setZone }), [zone, setZone]);
  return <TimeZoneContext.Provider value={value}>{children}</TimeZoneContext.Provider>;
}

export function useTimeZone(): TimeZoneContextValue {
  return useContext(TimeZoneContext);
}

// Time renders a timestamp in the chosen zone and puts the other zone
// in the title, so hovering answers "and in UTC?" without a toggle.
export function Time({ iso, style = "datetime", className }: { iso: string | undefined; style?: TimeStyle; className?: string }) {
  const { zone } = useTimeZone();
  if (!iso) {
    return <span className={cn("text-muted-foreground", className)}>—</span>;
  }
  const other = otherZone(zone);
  const title = style === "relative" ? formatTime(iso, { zone }) : `${formatTime(iso, { zone: other })} ${other.toUpperCase()}`;
  return (
    <time dateTime={iso} title={title} className={cn("font-mono tabular-nums", className)}>
      {formatTime(iso, { zone, style })}
    </time>
  );
}

// TimeZoneToggle is the top bar's Local / UTC switch.
export function TimeZoneToggle({ className }: { className?: string }) {
  const { zone, setZone } = useTimeZone();
  return (
    <div role="radiogroup" aria-label="Time zone" className={cn("inline-flex border border-border font-mono text-xs", className)}>
      {(["local", "utc"] as const).map((z) => (
        <button
          key={z}
          type="button"
          role="radio"
          aria-checked={zone === z}
          onClick={() => setZone(z)}
          className={cn("px-2 py-1", zone === z ? "bg-panel-2 text-foreground" : "text-muted-foreground hover:text-foreground")}
        >
          {z === "utc" ? "UTC" : "Local"}
        </button>
      ))}
    </div>
  );
}
