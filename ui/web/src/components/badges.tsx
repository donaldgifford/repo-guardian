import { humanize, toneFor } from "../lib/format";
import { Badge } from "./ui/badge";

// The five badges every view uses, each reading the tone table so no
// view carries a colour of its own (DESIGN-0036 §Badges).

export function StatusBadge({ status }: { status: string }) {
  return <Badge tone={toneFor("status", status)}>{status === "not_applicable" ? "n/a" : humanize(status)}</Badge>;
}

// RemediationBadge renders "—" for `none` inside a table, where a badge
// on every row would be noise; in a detail view it names it.
export function RemediationBadge({ remediation, table = false }: { remediation: string; table?: boolean }) {
  if (table && remediation === "none") {
    return <span className="text-muted-foreground">—</span>;
  }
  return <Badge tone={toneFor("remediation", remediation)}>{humanize(remediation)}</Badge>;
}

// ReasonBadge is toned by the status it sits under; the reason itself is
// an open code, shown verbatim.
export function ReasonBadge({ reason, status }: { reason: string; status: string }) {
  return <Badge tone={toneFor("reason", status)}>{reason}</Badge>;
}

export function KindBadge({ kind }: { kind: string }) {
  return <Badge tone={toneFor("kind", kind)}>{kind}</Badge>;
}

export function StateBadge({ state }: { state: string }) {
  return <Badge tone={toneFor("state", state)}>{humanize(state)}</Badge>;
}
