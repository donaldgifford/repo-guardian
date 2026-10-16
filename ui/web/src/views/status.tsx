import type { Schemas } from "../api/client";
import { useStatus, useUIConfig } from "../api/queries";
import { StateBadge } from "../components/badges";
import { Icon } from "../components/icons";
import { Time } from "../components/time";
import { Card } from "../components/ui/card";
import { Meter } from "../components/ui/meter";
import { flags } from "../lib/flags";
import { formatPercent } from "../lib/format";
import { cn } from "../lib/utils";

export type StatusTone = "ok" | "warn" | "bad" | "mute";

// statusHeadline maps the overall state onto the banner's fixed
// headline and tone (DESIGN-0036 §Status page). These four strings and
// the schema's own are the only text the page may show.
export function statusHeadline(state: string): { headline: string; tone: StatusTone } {
  switch (state) {
    case "operational":
      return { headline: "All systems operational", tone: "ok" };
    case "degraded":
      return { headline: "Degraded performance", tone: "warn" };
    case "unknown":
      return { headline: "Status not yet available", tone: "mute" };
    default:
      return { headline: "Service disruption", tone: "bad" };
  }
}

const toneVar: Record<StatusTone, string> = {
  ok: "var(--color-ok)",
  warn: "var(--color-warn)",
  bad: "var(--color-bad)",
  mute: "var(--color-muted-foreground)",
};

const dotClass: Record<StatusTone, string> = {
  ok: "bg-ok",
  warn: "bg-warn",
  bad: "bg-bad",
  mute: "bg-muted-foreground",
};

// StatusView is the public status page: outside the shell, no session,
// aggregate-only. Everything it prints comes from the Status schema or
// the headline map above.
export function StatusView() {
  const status = useStatus();
  const config = useUIConfig();
  const s: Schemas["Status"] = status.data ?? { state: "unknown", updated_at: "", components: [], compliance: { percent: null, measured: false } };
  const { headline, tone } = statusHeadline(s.state);
  return (
    <main className="mx-auto max-w-[760px] space-y-5 px-4 py-10">
      <header className="flex flex-wrap items-center justify-between gap-3">
        <h1 className="flex items-center gap-2 text-xl font-semibold">
          <Icon name="shield" className="size-5 text-accent" />
          Repo Guardian status
        </h1>
        <p className="text-xs text-muted-foreground">
          updated {s.updated_at ? <Time iso={s.updated_at} style="relative" /> : "—"} · refreshes every 30s
        </p>
      </header>

      <Card aria-label="Overall status" data-state={s.state} className="px-4 py-3.5" style={{ borderColor: toneVar[tone] }}>
        <div className="flex flex-wrap items-center gap-3">
          <span aria-hidden="true" className={cn("size-2.5 flex-none rounded-full", dotClass[tone], tone !== "mute" && "animate-pulse")} />
          <div className="min-w-0 flex-1">
            <h2 className="text-[15px] font-semibold">{headline}</h2>
            <p className="text-xs text-muted-foreground">Public, aggregate-only. No org, repository or rule names appear here.</p>
          </div>
          <StateBadge state={s.state} />
        </div>
      </Card>

      <div className="grid gap-4 md:grid-cols-5">
        <Card title="Components" bodyClassName="p-0" className="md:col-span-3">
          {s.components.length === 0 ? (
            <p className="px-3.5 py-2.5 text-sm text-muted-foreground">No components reported yet.</p>
          ) : (
            <ul className="divide-y divide-border">
              {s.components.map((c) => (
                <ComponentRow key={c.name} component={c} />
              ))}
            </ul>
          )}
          {s.components.some((c) => c.name === "webhooks") ? (
            <p className="border-t border-border px-3.5 py-2 text-xs text-muted-foreground">webhooks is informational: it reports deliveries but never moves the overall state.</p>
          ) : null}
        </Card>
        <Card title="Fleet compliance" className="md:col-span-2">
          <div className="text-[40px] leading-none font-semibold tabular-nums" data-measured={s.compliance.measured}>
            {s.compliance.measured ? formatPercent(s.compliance.percent) : "unmeasured"}
          </div>
          <Meter percent={s.compliance.measured ? s.compliance.percent : null} tone="ok" showValue={false} thick aria-label="Fleet compliance" className="mt-3" />
          <p className="mt-2 font-mono text-[11px] text-muted-foreground">
            measured: {String(s.compliance.measured)} · compliant ÷ (compliant + non_compliant)
          </p>
          {flags.statusExtras ? <Sparkline /> : null}
        </Card>
      </div>

      {flags.statusExtras ? <OperationsStrip /> : null}

      <footer className="flex flex-wrap items-center justify-between gap-3 text-xs text-muted-foreground">
        <code className="font-mono">GET /api/v1/status · served from an in-process cache</code>
        <span className="flex items-center gap-3">
          {config.data?.docs_url ? (
            <a href={config.data.docs_url} target="_blank" rel="noopener noreferrer" className="hover:text-foreground">
              Documentation ↗
            </a>
          ) : null}
          <a href="/auth/login" className="hover:text-foreground">
            Sign in →
          </a>
        </span>
      </footer>
    </main>
  );
}

function ComponentRow({ component: c }: { component: Schemas["Component"] }) {
  const { tone } = statusHeadline(c.state);
  return (
    <li className="flex items-center gap-3 px-3.5 py-2.5 text-[13px]" data-component={c.name}>
      <span aria-hidden="true" className={cn("size-2 flex-none rounded-full", dotClass[tone])} />
      <span className="font-mono font-medium">{c.name}</span>
      <span className="min-w-0 flex-1 truncate text-muted-foreground">{c.detail}</span>
      <StateBadge state={c.state} />
    </li>
  );
}

// Sparkline and OperationsStrip are built behind flags.statusExtras:
// the 30-day series and the three operations numbers need fields the
// public schema does not carry yet (IMPL-0031 7.4, DESIGN-0036 OQ10).
function Sparkline() {
  return <p className="mt-3 text-xs text-muted-foreground">30-day trend: awaiting compliance.history on the public schema.</p>;
}

function OperationsStrip() {
  const cells = ["Repositories checked in the last 24h", "Last policy rollout", "Last snapshot"];
  return (
    <Card className="grid grid-cols-1 divide-y divide-border md:grid-cols-3 md:divide-x md:divide-y-0" aria-label="Operations">
      {cells.map((label) => (
        <div key={label} className="px-3.5 py-2.5">
          <div className="text-xs text-muted-foreground">{label}</div>
          <div className="text-[22px] font-semibold tabular-nums">—</div>
        </div>
      ))}
    </Card>
  );
}
