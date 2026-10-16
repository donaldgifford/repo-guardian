import { formatPercent } from "../../lib/format";
import { cn } from "../../lib/utils";

export type MeterTone = "ok" | "warn" | "bad" | "mute";

// meterTone is the compliance threshold: bad under 70, warn under 90,
// ok from 90; unmeasured is mute.
export function meterTone(percent: number | null | undefined): MeterTone {
  if (percent === null || percent === undefined) return "mute";
  if (percent < 70) return "bad";
  if (percent < 90) return "warn";
  return "ok";
}

const fills: Record<MeterTone, string> = {
  ok: "var(--color-ok)",
  warn: "var(--color-warn)",
  bad: "var(--color-bad)",
  mute: "var(--color-muted-foreground)",
};

// Meter is a 5px bar on a 20% tint track with the percent printed after
// it; the tone follows the threshold unless the caller fixes one.
export function Meter({
  percent,
  tone,
  showValue = true,
  thick = false,
  className,
  "aria-label": label = "Compliance",
}: {
  percent: number | null | undefined;
  tone?: MeterTone;
  showValue?: boolean;
  // thick is the status page's 10px bar; the default is the 5px table meter.
  thick?: boolean;
  className?: string;
  // A meter needs a name; the table meters all measure compliance.
  "aria-label"?: string;
}) {
  const t = tone ?? meterTone(percent);
  const width = percent === null || percent === undefined ? 0 : Math.max(0, Math.min(100, percent));
  return (
    <div className={cn("flex items-center gap-2", className)}>
      <div
        role="meter"
        aria-label={label}
        aria-valuemin={0}
        aria-valuemax={100}
        aria-valuenow={width}
        aria-valuetext={formatPercent(percent)}
        data-tone={t}
        className={cn("min-w-20 flex-1 overflow-hidden rounded-[2px]", thick ? "h-[10px]" : "h-[5px]")}
        style={{ background: `color-mix(in srgb, ${fills[t]} 20%, transparent)` }}
      >
        <div className="h-full rounded-[2px]" style={{ width: `${width}%`, background: fills[t] }} />
      </div>
      {showValue ? <b className="font-medium tabular-nums">{formatPercent(percent)}</b> : null}
    </div>
  );
}
