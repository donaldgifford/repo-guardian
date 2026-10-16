import { Link } from "@tanstack/react-router";

import type { Schemas } from "../../api/client";
import { useRepository, useUIConfig } from "../../api/queries";
import { KindBadge, ReasonBadge, RemediationBadge, StatusBadge } from "../../components/badges";
import { ExternalLink, Loading } from "../../components/common";
import { Time } from "../../components/time";
import { Timeline } from "../../components/timeline";
import { Badge } from "../../components/ui/badge";
import { Button } from "../../components/ui/button";
import { DetailDrawer, DetailInset, DetailKV, DetailSection } from "../../components/ui/detail-drawer";
import { describeEvidence } from "../../lib/evidence";
import { prLink, repoLink } from "../../lib/pr";

export interface OpenKey {
  repositoryId: number;
  kind: string;
  name: string;
}

// parseOpenKey reads `?open=<repository_id>/<kind>/<name>`; a name may
// itself contain slashes. Anything malformed is undefined.
export function parseOpenKey(open: string | undefined): OpenKey | undefined {
  if (!open) return undefined;
  const [id, kind, ...rest] = open.split("/");
  const repositoryId = Number(id);
  const name = rest.join("/");
  if (!Number.isSafeInteger(repositoryId) || repositoryId <= 0 || !kind || name === "") return undefined;
  return { repositoryId, kind, name };
}

// findOpen picks the open finding out of the loaded rows.
export function findOpen(items: readonly Schemas["Finding"][], key: OpenKey): Schemas["Finding"] | undefined {
  return items.find((f) => f.repository_id === key.repositoryId && f.rule_kind === key.kind && f.rule_name === key.name);
}

export interface FindingDrawerProps {
  open: string | undefined;
  items: readonly Schemas["Finding"][];
  inline: boolean;
  onClose: () => void;
  returnFocusTo?: () => HTMLElement | null | undefined;
}

// FindingDrawer is the findings detail (DESIGN-0036 §Findings). The
// finding comes from the loaded pages; a pasted `?open=` whose row is
// not loaded is fetched through /repositories/{id} and matched by
// (kind, name).
export function FindingDrawer({ open, items, inline, onClose, returnFocusTo }: FindingDrawerProps) {
  const key = parseOpenKey(open);
  const loaded = key ? findOpen(items, key) : undefined;
  const fallback = useRepository(key?.repositoryId ?? 0, key !== undefined && loaded === undefined);
  const config = useUIConfig();
  if (!key) return null;
  const finding = loaded ?? (fallback.data ? findOpen(fallback.data.findings, key) : undefined);
  const host = config.data?.github_host;
  const title = finding ? `${finding.org}/${finding.repository}` : `repository #${key.repositoryId}`;
  const href = finding && host ? repoLink(host, finding.org, finding.repository) : undefined;
  return (
    <DetailDrawer
      open
      inline={inline}
      onClose={onClose}
      {...(returnFocusTo === undefined ? {} : { returnFocusTo })}
      aria-label="Finding"
      title={href ? <ExternalLink href={href}>{title}</ExternalLink> : title}
      subtitle={
        <span className="inline-flex items-center gap-2">
          {key.name}
          <KindBadge kind={key.kind} />
        </span>
      }
      footer={
        <>
          <span data-slot="actions" className="flex items-center gap-2" />
          <Link to="/repos/$id" params={{ id: String(key.repositoryId) }}>
            <Button variant="outline" size="sm" tabIndex={-1}>
              Open repository
            </Button>
          </Link>
        </>
      }
    >
      {finding ? (
        <FindingBody finding={finding} host={host} />
      ) : fallback.isError || (fallback.data && !finding) ? (
        <p className="text-sm text-muted-foreground">This finding is not visible: the repository is outside your orgs or the rule no longer applies.</p>
      ) : (
        <Loading />
      )}
    </DetailDrawer>
  );
}

function FindingBody({ finding: f, host }: { finding: Schemas["Finding"]; host: string | undefined }) {
  const evidence = describeEvidence(f);
  const pr = f.pr;
  const prHref = pr && host ? prLink(host, f.org, f.repository, pr.number) : undefined;
  const rows = [
    { key: "status", value: <StatusBadge status={f.status} /> },
    { key: "reason", value: f.reason ? <ReasonBadge reason={f.reason} status={f.status} /> : <span className="text-muted-foreground">—</span> },
    {
      key: "remediation",
      value: (
        <span className="inline-flex flex-wrap items-center gap-2">
          <RemediationBadge remediation={f.remediation} />
          {pr ? prHref ? <ExternalLink href={prHref}>#{pr.number}</ExternalLink> : <span className="font-mono">#{pr.number}</span> : null}
          {pr?.head ? <span className="font-mono text-xs text-muted-foreground">{pr.head}</span> : null}
          {pr?.matched_term ? <span className="font-mono text-xs text-muted-foreground">matched “{pr.matched_term}”</span> : null}
          {f.pr_stale ? <Badge tone="warn">stale</Badge> : null}
        </span>
      ),
    },
    { key: "since", value: <Time iso={f.status_since} /> },
    { key: "evaluated", value: <Time iso={f.last_evaluated_at} style="relative" /> },
  ];
  return (
    <>
      <DetailKV rows={rows} />
      {evidence ? (
        <DetailSection title="Evidence">
          <DetailInset aria-label="Evidence">
            <div className="text-foreground">{evidence.headline}</div>
            {evidence.lines.map((line, i) => (
              <div key={i} className="text-muted-foreground">
                {line}
              </div>
            ))}
          </DetailInset>
        </DetailSection>
      ) : null}
      <DetailSection title="Timeline">
        <Timeline
          repositoryId={f.repository_id}
          rule={{ kind: f.rule_kind, name: f.rule_name }}
          since={f.status_since}
          {...(pr ? { pr: { number: pr.number, ...(pr.created_at ? { created_at: pr.created_at } : {}), open: f.remediation === "pr_open" || f.remediation === "foreign_pr" } } : {})}
        />
      </DetailSection>
    </>
  );
}
