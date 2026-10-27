import type { Schemas } from "../api/client";

// EvidenceView is a rule result's evidence as plain text: a headline and
// detail lines. Nothing here is ever rendered as markup; values that
// came from a repository stay strings.
export interface EvidenceView {
  headline: string;
  lines: string[];
}

const scalar = (v: unknown): v is string | number | boolean => typeof v === "string" || typeof v === "number" || typeof v === "boolean";

// maxLines bounds what one rule's evidence prints.
const maxLines = 8;

// describeEvidence renders evidence by its envelope: framework evidence
// (an evaluation that could not decide) by its reason, control evidence
// by its kind with its flat scalar fields as `key: value` lines, sorted.
// A field that is not a scalar is skipped rather than guessed at.
export function describeEvidence(e: Schemas["Evidence"]): EvidenceView {
  const fields = e as Record<string, unknown>;
  const reason = fields.reason;
  const headline = typeof reason === "string" && reason !== "" ? reason : e.evidence_kind;
  const lines = Object.keys(fields)
    .filter((k) => k !== "evidence_kind" && k !== "evidence_version" && k !== "reason")
    .sort()
    .flatMap((k) => {
      const v = fields[k];
      if (scalar(v)) return [`${k}: ${String(v)}`];
      if (Array.isArray(v) && v.every(scalar)) return [`${k}: ${v.map(String).join(", ")}`];
      return [];
    });
  return { headline, lines: lines.slice(0, maxLines) };
}
