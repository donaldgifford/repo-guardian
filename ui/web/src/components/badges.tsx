import type { Schemas } from "../api/client";
import { ageDays, humanize, toneFor } from "../lib/format";
import { holdSentence } from "../lib/holds";
import { ExternalLink } from "./posture";
import { Badge } from "./ui/badge";

// The badges every view uses, each reading the tone table so no
// view carries a colour of its own (DESIGN-0036 §Badges).

export function StatusBadge({ status }: { status: string }) {
  return <Badge tone={toneFor("status", status)}>{status === "not_applicable" ? "n/a" : humanize(status)}</Badge>;
}

// RuleStatusBadge is a rule result's status (pass, fail, error, unknown,
// not_applicable), toned by the `rule` row, not the control `status` row.
export function RuleStatusBadge({ status }: { status: string }) {
  return <Badge tone={toneFor("rule", status)}>{status === "not_applicable" ? "n/a" : humanize(status)}</Badge>;
}

// staleAfterDays is when an open pull request reads stale (the API's
// pr_stale threshold).
export const staleAfterDays = 30;

// RemediationStateBadge is a remediation state alone, toned by the
// `remediation` row; `open` reads "pr open".
export function RemediationStateBadge({ state }: { state: string }) {
  return <Badge tone={toneFor("remediation", state)}>{state === "open" ? "pr open" : humanize(state)}</Badge>;
}

// RemediationBadge is a result's latest remediation (IMPL-0032 6.3),
// shared by Findings, the drawer and the repository page: the state,
// the pull request number as a link, its age and a stale badge once an
// open pull request is 30 days old. Absent, it is "—".
export function RemediationBadge({ remediation: r, now }: { remediation: Pick<Schemas["ResultRemediation"], "state" | "kind" | "created_at" | "pr_number" | "pr_url"> | undefined; now?: Date }) {
  if (!r) return <span className="text-muted-foreground">—</span>;
  const age = ageDays(r.created_at, now);
  const stale = r.kind === "pull_request" && r.state === "open" && age >= staleAfterDays;
  return (
    <span className="inline-flex flex-wrap items-center gap-1.5">
      <RemediationStateBadge state={r.state} />
      {r.pr_number !== undefined ? r.pr_url ? <ExternalLink href={r.pr_url}>#{r.pr_number}</ExternalLink> : <span className="font-mono">#{r.pr_number}</span> : null}
      <span className="text-xs text-muted-foreground tabular-nums">{age}d</span>
      {stale ? <Badge tone="warn">stale</Badge> : null}
    </span>
  );
}

// ReasonBadge is toned by the status it sits under; the reason itself is
// an open code, shown verbatim.
export function ReasonBadge({ reason, status }: { reason: string; status: string }) {
  return <Badge tone={toneFor("reason", status)}>{reason}</Badge>;
}

export function KindBadge({ kind }: { kind: string }) {
  return <Badge tone={toneFor("kind", kind)}>{kind}</Badge>;
}

// HoldBadge is a hold reason with its sentence as the title; an
// unknown hold renders its code, never blank (IMPL-0032 6.2).
export function HoldBadge({ hold }: { hold: string }) {
  return (
    <Badge tone={toneFor("hold", hold)} title={holdSentence(hold)}>
      {humanize(hold)}
    </Badge>
  );
}

export function StateBadge({ state }: { state: string }) {
  return <Badge tone={toneFor("state", state)}>{humanize(state)}</Badge>;
}
