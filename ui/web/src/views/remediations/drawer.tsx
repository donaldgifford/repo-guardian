import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";

import type { Schemas } from "../../api/client";
import { fetchAllRemediations, openPullRequestsCap, useRepositoryEvents } from "../../api/queries";
import { HoldBadge, RemediationStateBadge } from "../../components/badges";
import { Loading } from "../../components/common";
import { useCrumbLabel } from "../../components/page/Breadcrumbs";
import { ExternalLink } from "../../components/posture";
import { Time } from "../../components/time";
import { Badge } from "../../components/ui/badge";
import { Button } from "../../components/ui/button";
import { DetailDrawer, DetailKV, DetailSection } from "../../components/ui/detail-drawer";
import { humanize } from "../../lib/format";
import { holdSentence } from "../../lib/holds";
import { reassignedEvents, StatusMove, timelineEvents } from "../findings/drawer";
import { isStale, matchesKey, type OpenRemediation, parseRemediationKey, remediationAge } from "./model";

export interface RemediationDrawerProps {
  open: string | undefined;
  // loaded is the table's rows: the row is taken from them when present,
  // else looked up by paging the list, so a pasted link works.
  loaded: readonly Schemas["Remediation"][];
  inline: boolean;
  onClose: () => void;
  returnFocusTo?: () => HTMLElement | null | undefined;
}

// useRemediationByKey finds one row for a deep link the table has not
// loaded: there is no GET /remediations/{id}, so it pages the list
// (held results under every hold) up to the cap.
function useRemediationByKey(key: OpenRemediation | undefined, enabled: boolean) {
  return useQuery({
    queryKey: ["remediation", key ?? ""],
    queryFn: async () => {
      if (!key) return null;
      const listing = "id" in key ? fetchAllRemediations({}, openPullRequestsCap) : fetchAllRemediations({ hold: ["pr_cap", "foreign_branch", "stale_branch", "conflict", "permission"] }, openPullRequestsCap);
      const { rows } = await listing;
      return rows.find((r) => matchesKey(r, key)) ?? null;
    },
    enabled: enabled && key !== undefined,
  });
}

// RemediationDrawer is one remediation (DESIGN-0037 §Remediations
// view): state, the pull request and its branch, the hold with its
// sentence, age, a recommendation's proposal, a direct apply's steps,
// the result's timeline, and links to the finding and the repository.
export function RemediationDrawer({ open, loaded, inline, onClose, returnFocusTo }: RemediationDrawerProps) {
  const key = parseRemediationKey(open);
  const fromTable = key ? loaded.find((r) => matchesKey(r, key)) : undefined;
  const lookup = useRemediationByKey(key, fromTable === undefined);
  const r = fromTable ?? lookup.data ?? undefined;
  // The open row's crumb reads `org/name · control` (IMPL-0032 6.1).
  useCrumbLabel(open ?? "", r ? `${r.org}/${r.repository} · ${r.control_id}` : undefined);
  if (!key) return null;
  const title = r ? `${r.org}/${r.repository}` : `remediation ${open}`;
  return (
    <DetailDrawer
      open
      inline={inline}
      onClose={onClose}
      {...(returnFocusTo === undefined ? {} : { returnFocusTo })}
      aria-label={`Remediation: ${title}${r ? ` ${r.control_id}` : ""}`}
      title={title}
      subtitle={
        r ? (
          <span>
            {r.control_id} · {humanize(r.kind)} · opened <Time iso={r.created_at} style="date" />
          </span>
        ) : undefined
      }
      footer={
        r ? (
          <>
            <Link to="/findings" search={{ control: r.control_id, open: `${r.repository_id}/${r.control_id}` }}>
              <Button variant="outline" size="sm" tabIndex={-1}>
                Open finding →
              </Button>
            </Link>
            <Link to="/repositories/$id" params={{ id: String(r.repository_id) }}>
              <Button variant="outline" size="sm" tabIndex={-1}>
                Open repository →
              </Button>
            </Link>
          </>
        ) : undefined
      }
    >
      {r ? <RemediationBody remediation={r} /> : lookup.isFetched || lookup.isError ? <p className="text-sm text-muted-foreground">This remediation is not visible: it is outside your orgs, past the listing cap, or gone.</p> : <Loading />}
    </DetailDrawer>
  );
}

// RemediationFacts is the KV block shared with the repository panel.
export function remediationRows(r: Schemas["Remediation"]) {
  const age = remediationAge(r);
  return [
    {
      key: "state",
      value: (
        <span className="inline-flex flex-wrap items-center gap-2">
          {r.state ? <RemediationStateBadge state={r.state} /> : <Badge tone="mute">held</Badge>}
          {isStale(r) ? <Badge tone="warn">stale</Badge> : null}
        </span>
      ),
    },
    ...(r.pr_number !== undefined
      ? [
          {
            key: "pull request",
            value: (
              <span className="inline-flex flex-wrap items-center gap-2">
                {r.pr_url ? <ExternalLink href={r.pr_url}>#{r.pr_number}</ExternalLink> : <span className="font-mono">#{r.pr_number}</span>}
                <span className="font-mono text-xs text-muted-foreground">{r.branch ?? `repo-guardian/${r.control_id}`}</span>
              </span>
            ),
          },
        ]
      : []),
    {
      key: "hold",
      value: r.hold ? (
        <span className="inline-flex flex-wrap items-center gap-2">
          <HoldBadge hold={r.hold} />
          <span className="text-xs text-muted-foreground">{holdSentence(r.hold)}</span>
        </span>
      ) : (
        <span className="text-muted-foreground">—</span>
      ),
    },
    { key: "age", value: `${age} ${age === 1 ? "day" : "days"}` },
    ...(r.closed_at ? [{ key: "closed", value: <span><Time iso={r.closed_at} style="relative" />{r.close_reason ? ` · ${humanize(r.close_reason)}` : ""}</span> }] : []),
  ];
}

const json = (v: unknown) => (v === undefined ? "—" : JSON.stringify(v));

// StepsJournal lists a direct apply's journaled steps in order.
export function StepsJournal({ steps }: { steps: readonly Schemas["RemediationStep"][] }) {
  return (
    <ol className="space-y-1 font-mono text-xs" aria-label="Steps">
      {steps.map((st) => (
        <li key={st.seq} className="flex flex-wrap items-baseline gap-2">
          <span className="text-muted-foreground">{st.seq}.</span>
          <span>{st.kind}</span>
          <Badge tone={st.status === "failed" ? "bad" : "ok"}>{st.status}</Badge>
          {st.resource_key ? <span className="text-muted-foreground">{st.resource_key}</span> : null}
          {st.pr_number !== undefined ? <span className="text-muted-foreground">#{st.pr_number}</span> : null}
          {st.committed_sha ? <span className="text-muted-foreground">{st.committed_sha.slice(0, 12)}</span> : null}
          {st.error ? <span className="break-all text-bad">{st.error}</span> : null}
        </li>
      ))}
    </ol>
  );
}

function RemediationBody({ remediation: r }: { remediation: Schemas["Remediation"] }) {
  const events = useRepositoryEvents(r.repository_id);
  const timeline = timelineEvents(events.data?.items ?? [], r.control_id);
  const reassigned = reassignedEvents(events.data?.items ?? []);
  return (
    <>
      <DetailKV rows={remediationRows(r)} />
      {r.proposal && r.proposal.length > 0 ? (
        <DetailSection title={r.state === "recommended" ? "Proposal (recommend: a human applies it)" : "Proposal"}>
          <ul className="space-y-1 font-mono text-xs" aria-label="Proposal">
            {r.proposal.map((c) => (
              <li key={c.key} className="flex flex-wrap items-baseline gap-2">
                <span>{c.key}</span>
                <span className="text-muted-foreground">{c.op}</span>
                <span className="text-muted-foreground">{json(c.before)}</span>
                <span aria-hidden="true">→</span>
                <span>{json(c.after)}</span>
              </li>
            ))}
          </ul>
        </DetailSection>
      ) : null}
      {r.steps.length > 0 ? (
        <DetailSection title="Steps (journal)">
          <StepsJournal steps={r.steps} />
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
                {e.kind ? (
                  <span className="font-mono">{humanize(e.kind)}</span>
                ) : (
                  <StatusMove event={e} reassigned={reassigned.has(e.id)} />
                )}
              </li>
            ))}
          </ol>
        )}
      </DetailSection>
    </>
  );
}
