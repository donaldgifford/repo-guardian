import { Link } from "@tanstack/react-router";

import type { Schemas } from "../../api/client";
import { useRepository, useRepositoryControls, useRepositoryEvaluations, useRepositoryEvents, useUIConfig } from "../../api/queries";
import { RemediationBadge, RuleStatusBadge, StatusBadge } from "../../components/badges";
import { Loading } from "../../components/common";
import { GatedPanel } from "../../components/GatedPanel";
import { BucketBadge, EvidenceText, ExternalLink } from "../../components/posture";
import { Time } from "../../components/time";
import { Button } from "../../components/ui/button";
import { type Column, DataTable } from "../../components/ui/data-table";
import { DetailDrawer, DetailKV, DetailSection } from "../../components/ui/detail-drawer";
import { humanize } from "../../lib/format";
import { holdSentence } from "../../lib/holds";
import { openSearch } from "../remediations/model";
import { EvaluateButton } from "../repositories";

export interface OpenKey {
  repositoryId: number;
  control: string;
}

// parseOpenKey reads `?open=<repository_id>/<control>`. Anything
// malformed is undefined.
export function parseOpenKey(open: string | undefined): OpenKey | undefined {
  if (!open) return undefined;
  const slash = open.indexOf("/");
  if (slash <= 0) return undefined;
  const repositoryId = Number(open.slice(0, slash));
  const control = open.slice(slash + 1);
  if (!Number.isSafeInteger(repositoryId) || repositoryId <= 0 || control === "") return undefined;
  return { repositoryId, control };
}

// findingKey is a finding's identity in the table and in `?open=`.
export function findingKey(r: Pick<Schemas["ResultRow"], "repository_id" | "control_id">): string {
  return `${r.repository_id}/${r.control_id}`;
}

// repositoryEventKinds are the repository events worth a place in a
// finding's timeline beside the control's own.
const repositoryEventKinds = new Set(["parked", "unparked", "renamed", "transferred"]);

// timelineEvents keeps the control's result and remediation events and
// the repository's parks and renames, newest first as the API lists
// them, bounded.
export function timelineEvents(events: readonly Schemas["Event"][], control: string, max = 12): Schemas["Event"][] {
  return events.filter((e) => (e.source === "repository" ? repositoryEventKinds.has(e.kind ?? "") : e.control_id === control)).slice(0, max);
}

// reassignedEvents names the result events that start a status over: no
// from_status, with an older event for the same control and rule
// further down the list. A policy change that re-assigns a control (a
// new revision, so a new assignment epoch) records its next result as a
// first one; the timeline says "re-assigned" for those rather than a
// second "new". Events are newest first, as the API lists them.
export function reassignedEvents(events: readonly Schemas["Event"][]): Set<number> {
  const out = new Set<number>();
  const seen = new Set<string>();
  for (let i = events.length - 1; i >= 0; i--) {
    const e = events[i];
    if (!e || e.source !== "result") continue;
    const key = `${e.control_id ?? ""}/${e.rule_id ?? ""}`;
    if (e.from_status === undefined && e.to_status !== undefined && seen.has(key)) out.add(e.id);
    seen.add(key);
  }
  return out;
}

// evidenceGroups folds failing rules that carry the same evidence into
// one block, in first-seen order: a missing file fails every rule that
// reads it with the same reason and locations.
export function evidenceGroups(rules: readonly Schemas["RuleResult"][]): { rules: string[]; evidence: Schemas["Evidence"] }[] {
  const groups = new Map<string, { rules: string[]; evidence: Schemas["Evidence"] }>();
  for (const r of rules) {
    const key = JSON.stringify(r.evidence);
    const g = groups.get(key);
    if (g) g.rules.push(r.rule_id);
    else groups.set(key, { rules: [r.rule_id], evidence: r.evidence });
  }
  return [...groups.values()];
}

const isFailing = (r: Schemas["RuleResult"]) => r.status === "fail" || r.status === "error";

export interface FindingDrawerProps {
  open: string | undefined;
  inline: boolean;
  onClose: () => void;
  returnFocusTo?: () => HTMLElement | null | undefined;
}

// FindingDrawer is the finding detail (DESIGN-0037 §Findings): the
// control's result on one repository, from /repositories/{id} and its
// controls, events and evaluations. A pasted `?open=` works on its own:
// nothing here depends on the table's rows.
export function FindingDrawer({ open, inline, onClose, returnFocusTo }: FindingDrawerProps) {
  const key = parseOpenKey(open);
  const id = key?.repositoryId ?? 0;
  const repository = useRepository(id);
  const controls = useRepositoryControls(id);
  const config = useUIConfig();
  if (!key) return null;
  const r = repository.data?.repository;
  const control = controls.data?.items.find((c) => c.control_id === key.control);
  const title = r ? `${r.org}/${r.name}` : `repository #${key.repositoryId}`;
  const href = r && config.data ? `https://${config.data.github_host}/${r.org}/${r.name}` : undefined;
  const missing = (repository.isError || controls.isError || (controls.data && !control)) ?? false;
  return (
    <DetailDrawer
      open
      inline={inline}
      onClose={onClose}
      {...(returnFocusTo === undefined ? {} : { returnFocusTo })}
      aria-label={`Finding: ${title} ${key.control}`}
      title={href ? <ExternalLink href={href}>{title}</ExternalLink> : title}
      subtitle={
        control ? (
          <span>
            {control.control_id} v{control.version} · {humanize(control.decision)}
            {control.last_changed_at ? (
              <>
                {" "}
                · failing since <Time iso={control.last_changed_at} style="date" />
              </>
            ) : null}
          </span>
        ) : (
          key.control
        )
      }
      footer={
        <>
          <span className="flex items-center gap-2">{r?.active ? <EvaluateButton id={r.id} /> : null}</span>
          <Link to="/repositories/$id" params={{ id: String(key.repositoryId) }}>
            <Button variant="outline" size="sm" tabIndex={-1}>
              Open repository →
            </Button>
          </Link>
        </>
      }
    >
      {control ? (
        <FindingBody repositoryId={key.repositoryId} control={control} />
      ) : missing ? (
        <p className="text-sm text-muted-foreground">This finding is not visible: the repository is outside your orgs or the control is no longer assigned.</p>
      ) : (
        <Loading />
      )}
    </DetailDrawer>
  );
}

const ruleColumns: Column<Schemas["RuleResult"]>[] = [
  { key: "rule", header: "Rule", cell: (r) => <span className="font-mono">{r.rule_id}</span> },
  { key: "status", header: "Status", cell: (r) => <RuleStatusBadge status={r.status} /> },
  { key: "remediate", header: "Remediable rule", cell: (r) => (r.remediate ? "yes" : "no"), numeric: true },
];

// remediationOf lifts a RepositoryControl's latest remediation into the
// shape RemediationBadge reads; a held result (no state) is not shown
// as a remediation.
export function remediationOf(c: Schemas["RepositoryControl"]): Schemas["ResultRemediation"] | undefined {
  const r = c.remediation;
  if (!r || r.state === undefined) return undefined;
  return { kind: r.kind, state: r.state, created_at: r.created_at, age_days: 0, ...(r.pr_number === undefined ? {} : { pr_number: r.pr_number }), ...(r.pr_url === undefined ? {} : { pr_url: r.pr_url }) };
}

function FindingBody({ repositoryId, control: c }: { repositoryId: number; control: Schemas["RepositoryControl"] }) {
  const events = useRepositoryEvents(repositoryId);
  const evaluations = useRepositoryEvaluations(repositoryId);
  const failing = c.rules.filter(isFailing);
  const rows = [
    {
      key: "status",
      value: (
        <span className="inline-flex items-center gap-2">
          {c.status ? <StatusBadge status={c.status} /> : null}
          <BucketBadge bucket={c.bucket} />
        </span>
      ),
    },
    {
      key: "remediation",
      value: (
        <GatedPanel isEmpty={false} empty={null} className="text-sm text-muted-foreground">
          <span className="inline-flex flex-wrap items-center gap-2">
            <RemediationBadge remediation={remediationOf(c)} />
            {c.hold ? <span className="text-xs text-muted-foreground">{holdSentence(c.hold)}</span> : null}
            {c.remediation ? (
              <Link to="/remediations" search={{ open: openSearch(c.remediation) }} className="text-xs text-accent hover:underline">
                Open remediation →
              </Link>
            ) : null}
          </span>
        </GatedPanel>
      ),
    },
    { key: "last evaluated", value: c.evaluated_at ? <Time iso={c.evaluated_at} style="relative" /> : <span className="text-muted-foreground">never</span> },
  ];
  const timeline = timelineEvents(events.data?.items ?? [], c.control_id);
  const reassigned = reassignedEvents(events.data?.items ?? []);
  const recent = (evaluations.data?.items ?? []).slice(0, 2);
  return (
    <>
      <DetailKV rows={rows} />
      <DetailSection title="Rules">
        <div className="rounded-lg border border-border">
          <DataTable columns={ruleColumns} rows={c.rules} rowKey={(r) => r.rule_id} empty="Not evaluated yet." aria-label={`${c.control_id} rules`} />
        </div>
      </DetailSection>
      {failing.length > 0 ? (
        <DetailSection title="Evidence">
          <div className="space-y-2">
            {evidenceGroups(failing).map((g) => (
              <div key={g.rules.join(",")} className="rounded-lg border border-border bg-background px-3 py-2" data-rule={g.rules.join(" ")}>
                <div className="mb-1 font-mono text-xs text-muted-foreground">{g.rules.join(", ")}</div>
                <EvidenceText evidence={g.evidence} />
              </div>
            ))}
          </div>
        </DetailSection>
      ) : null}
      <DetailSection title="Timeline">
        {!events.data ? (
          <Loading />
        ) : timeline.length === 0 ? (
          <p className="text-xs text-muted-foreground">No events yet.</p>
        ) : (
          <ol className="space-y-1.5 text-[13px]" aria-label="Timeline">
            {timeline.map((e) => (
              <li key={`${e.source}/${e.id}`} className="flex items-baseline gap-3">
                <Time iso={e.occurred_at} style="relative" className="w-24 flex-none text-xs text-muted-foreground" />
                <TimelineText event={e} reassigned={reassigned.has(e.id)} />
              </li>
            ))}
          </ol>
        )}
      </DetailSection>
      <DetailSection title="Last evaluations">
        {!evaluations.data ? (
          <Loading />
        ) : recent.length === 0 ? (
          <p className="text-xs text-muted-foreground">No evaluation yet.</p>
        ) : (
          <ul className="space-y-1 text-[13px]" aria-label="Last evaluations">
            {recent.map((ev) => (
              <li key={ev.id} className="flex items-baseline gap-3">
                <Time iso={ev.started_at} style="relative" className="w-24 flex-none text-xs text-muted-foreground" />
                <span>
                  {humanize(ev.trigger)} · {ev.outcome}
                  {ev.error ? <span className="ml-1 font-mono text-xs break-all text-muted-foreground">{ev.error}</span> : null}
                </span>
              </li>
            ))}
          </ul>
        )}
      </DetailSection>
    </>
  );
}

// TimelineText is one event as a sentence: a rule or control status
// move, or a repository or remediation event by kind. A first status
// reads "new", or "re-assigned" when it follows an earlier one.
function TimelineText({ event: e, reassigned = false }: { event: Schemas["Event"]; reassigned?: boolean }) {
  if (e.kind) {
    return (
      <span>
        <span className="font-mono">{humanize(e.kind)}</span>
        {e.source === "remediation" ? <span className="text-muted-foreground"> · remediation</span> : null}
      </span>
    );
  }
  return <StatusMove event={e} reassigned={reassigned} />;
}

// StatusMove is a result event's status transition, shared with the
// remediation drawer's timeline.
export function StatusMove({ event: e, reassigned = false }: { event: Schemas["Event"]; reassigned?: boolean }) {
  const Badge = e.rule_id ? RuleStatusBadge : StatusBadge;
  return (
    <span className="inline-flex flex-wrap items-center gap-1.5">
      {e.rule_id ? <span className="font-mono text-xs">{e.rule_id}</span> : null}
      {e.from_status ? <Badge status={e.from_status} /> : <span className="text-muted-foreground">{reassigned ? "re-assigned" : "new"}</span>}→{e.to_status ? <Badge status={e.to_status} /> : <span className="text-muted-foreground">cleared</span>}
    </span>
  );
}
