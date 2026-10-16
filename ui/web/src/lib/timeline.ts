import type { Schemas } from "../api/client";
import { ageDays, humanize } from "./format";

type Event = Schemas["Event"];
type Check = Schemas["Check"];

export type TimelineTone = "accent" | "ok" | "bad" | "mute";

export interface TimelineNode {
  key: string;
  at: string;
  label: string;
  detail?: string;
  tone: TimelineTone;
  // source tells a renderer what kind of node this is; "pr" is the
  // synthesised "still open" node.
  source: "finding" | "repository" | "check" | "pr";
}

export interface Timeline {
  // nodes are oldest first, so the latest is last and drawn in accent.
  nodes: TimelineNode[];
  total: number;
  truncated: boolean;
}

export const TIMELINE_CAP = 20;

// REPOSITORY_KINDS are the repository events a finding's timeline shows
// beside its own finding events (DESIGN-0036 §Findings).
export const REPOSITORY_KINDS = ["parked", "unparked", "renamed", "transferred"] as const;

export interface TimelineInput {
  events: readonly Event[];
  checks: readonly Check[];
  // rule scopes finding events to one rule (the drawer); absent, every
  // event is shown (the repository page).
  rule?: { kind: string; name: string };
  // since drops checks before the finding's status_since: a check that
  // predates the status did not produce it.
  since?: string;
  // pr adds "PR #n still open (<age>)" after an open PR's created_at.
  pr?: { number: number; created_at?: string; open: boolean };
  now?: Date;
  cap?: number;
}

function statusTone(status: string | undefined): TimelineTone {
  if (status === "compliant") return "ok";
  if (status === "non_compliant") return "bad";
  return "mute";
}

function describeFindingEvent(e: Event): Pick<TimelineNode, "label" | "detail" | "tone"> {
  const from = e.from_status ? `${humanize(e.from_status)} → ` : "";
  const to = e.to_status ? humanize(e.to_status) : "cleared";
  const bits = [e.to_reason, e.to_remediation && e.to_remediation !== "none" ? e.to_remediation : undefined].filter((s): s is string => Boolean(s));
  return { label: `${e.rule_name ?? "rule"}: ${from}${to}`, ...(bits.length > 0 ? { detail: bits.join(" · ") } : {}), tone: statusTone(e.to_status) };
}

function describeRepositoryEvent(e: Event): Pick<TimelineNode, "label" | "detail" | "tone"> {
  const d = (e.detail ?? {}) as Record<string, unknown>;
  const pairs = Object.entries(d)
    .filter(([, v]) => typeof v === "string" || typeof v === "number")
    .map(([k, v]) => `${k} ${String(v)}`);
  return { label: humanize(e.kind ?? e.source), ...(pairs.length > 0 ? { detail: pairs.join(" · ") } : {}), tone: e.kind === "parked" || e.kind === "removed" ? "bad" : "mute" };
}

function checkTone(outcome: string): TimelineTone {
  if (outcome === "error") return "bad";
  if (outcome === "success") return "ok";
  return "mute";
}

// buildTimeline merges finding events, repository events and checks
// into one oldest-first list, synthesises the open-PR node, and keeps
// the latest `cap` nodes.
export function buildTimeline({ events, checks, rule, since, pr, now = new Date(), cap = TIMELINE_CAP }: TimelineInput): Timeline {
  const nodes: TimelineNode[] = [];
  for (const e of events) {
    if (e.source === "finding") {
      if (rule && (e.rule_kind !== rule.kind || e.rule_name !== rule.name)) continue;
      nodes.push({ key: `finding/${e.id}`, at: e.occurred_at, source: "finding", ...describeFindingEvent(e) });
    } else {
      if (rule && !(REPOSITORY_KINDS as readonly string[]).includes(e.kind ?? "")) continue;
      nodes.push({ key: `repository/${e.id}`, at: e.occurred_at, source: "repository", ...describeRepositoryEvent(e) });
    }
  }
  const sinceMs = since ? new Date(since).getTime() : Number.NEGATIVE_INFINITY;
  for (const c of checks) {
    if (new Date(c.started_at).getTime() < sinceMs) continue;
    nodes.push({
      key: `check/${c.id}`,
      at: c.started_at,
      source: "check",
      label: `re-checked · ${c.outcome}`,
      ...(c.error ? { detail: c.error } : {}),
      tone: checkTone(c.outcome),
    });
  }
  if (pr?.open && pr.created_at) {
    nodes.push({ key: `pr/${pr.number}`, at: now.toISOString(), source: "pr", label: `PR #${pr.number} still open (${ageDays(pr.created_at, now)}d)`, tone: "accent" });
  }
  nodes.sort((a, b) => a.at.localeCompare(b.at) || a.key.localeCompare(b.key));
  const total = nodes.length;
  const kept = total > cap ? nodes.slice(total - cap) : nodes;
  return { nodes: kept, total, truncated: total > cap };
}
