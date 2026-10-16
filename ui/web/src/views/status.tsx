import type { ReactNode } from "react";

import type { Schemas } from "../api/client";
import { useStatus, useUIConfig } from "../api/queries";
import { StateBadge } from "../components/badges";
import { Sparkline } from "../components/charts";
import { Icon } from "../components/icons";
import { Time } from "../components/time";
import { Card } from "../components/ui/card";
import { Meter } from "../components/ui/meter";
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
  const s: Schemas["Status"] = status.data ?? { state: "unknown", updated_at: "", components: [], compliance: { percent: null, measured: false, history: [] }, operations: { checked_24h: 0, tracked: 0, rollout_state: "none" } };
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
          <ComplianceTrend history={s.compliance.history} />
        </Card>
      </div>

      <OperationsStrip ops={s.operations} />

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

// ComplianceTrend is the 30-day fleet line from compliance.history, one
// point per day's last snapshot. The sparkline is aria-hidden; the
// sentence beside it carries the first and last values.
function ComplianceTrend({ history }: { history: Schemas["CompliancePoint"][] }) {
  const values = history.map((p) => p.percent ?? null);
  const measured = values.filter((v): v is number => v !== null);
  const first = measured[0];
  const last = measured[measured.length - 1];
  return (
    <div className="mt-3 flex items-center justify-between gap-3" data-testid="status-trend">
      <span className="text-xs text-muted-foreground">
        last 30 days
        {first !== undefined && last !== undefined && measured.length > 1 ? (
          <span className="sr-only">
            {`: from ${formatPercent(first)} to ${formatPercent(last)}`}
          </span>
        ) : null}
      </span>
      <Sparkline values={values} width={180} height={28} color="var(--color-ok)" />
    </div>
  );
}

// rolloutLabel maps the closed rollout_state enum onto fixed text.
export function rolloutLabel(ops: Schemas["StatusOperations"]): { text: string; at?: string } {
  switch (ops.rollout_state) {
    case "complete":
      return ops.rollout_completed_at ? { text: "complete", at: ops.rollout_completed_at } : { text: "complete" };
    case "in_progress":
      return { text: "in progress" };
    default:
      return { text: "—" };
  }
}

// OperationsStrip is three aggregate numbers: repositories checked in
// the last 24h of those tracked, the newest policy's rollout and the
// last compliance snapshot.
function OperationsStrip({ ops }: { ops: Schemas["StatusOperations"] }) {
  const rollout = rolloutLabel(ops);
  return (
    <Card className="grid grid-cols-1 divide-y divide-border md:grid-cols-3 md:divide-x md:divide-y-0" aria-label="Operations">
      <OperationsCell label="Repositories checked in the last 24h">
        {ops.checked_24h.toLocaleString()}
        <span className="text-sm font-normal text-muted-foreground"> / {ops.tracked.toLocaleString()}</span>
      </OperationsCell>
      <OperationsCell label="Last policy rollout">{rollout.at ? <Time iso={rollout.at} style="relative" /> : rollout.text}</OperationsCell>
      <OperationsCell label="Last snapshot">{ops.last_snapshot_at ? <Time iso={ops.last_snapshot_at} style="relative" /> : "—"}</OperationsCell>
    </Card>
  );
}

function OperationsCell({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="px-3.5 py-2.5">
      <div className="text-xs text-muted-foreground">{label}</div>
      <div className="text-[22px] font-semibold tabular-nums">{children}</div>
    </div>
  );
}
