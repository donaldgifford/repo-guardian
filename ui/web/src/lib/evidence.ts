import type { Schemas } from "../api/client";

// EvidenceView is a finding's evidence as plain text: a headline and
// detail lines. Nothing here is ever rendered as markup; values that
// came from a repository stay strings.
export interface EvidenceView {
  reason: string;
  headline: string;
  lines: string[];
}

type Finding = Pick<Schemas["Finding"], "reason" | "evidence">;
type Fields = Record<string, unknown>;

const str = (v: unknown): v is string => typeof v === "string";
const strs = (v: unknown): v is string[] => Array.isArray(v) && v.every(str);

// renderers holds one renderer per known reason. Each checks the shape
// it needs and returns undefined when the fields are not what this
// version of the UI understands, so unknown or newer evidence falls
// back to the reason code alone rather than to a guess.
const renderers: Record<string, (e: Fields) => Omit<EvidenceView, "reason"> | undefined> = {
  file_missing: (e) => (strs(e.paths_checked) ? { headline: "file missing", lines: e.paths_checked.map((p) => `checked ${p}`) } : undefined),
  assertion_failed: (e) => (str(e.path) && str(e.message) ? { headline: `assertion failed in ${e.path}`, lines: [e.message] } : undefined),
  content_differs: (e) =>
    str(e.path) && str(e.comparison) ? { headline: `${e.path} differs from the template`, lines: [`compared as ${e.comparison}`] } : undefined,
  forbidden_present: (e) => (str(e.path) ? { headline: `forbidden file present: ${e.path}`, lines: [] } : undefined),
  setting_mismatch: (e) =>
    str(e.property) && str(e.expected) && str(e.actual)
      ? { headline: `setting ${e.property}`, lines: [`expected ${e.expected}`, `actual ${e.actual}`] }
      : undefined,
  ruleset_missing: (e) => (str(e.branch) ? { headline: `no ruleset covers ${e.branch}`, lines: [] } : undefined),
  ruleset_mismatch: (e) =>
    str(e.branch) && strs(e.mismatches) ? { headline: `ruleset on ${e.branch} differs`, lines: e.mismatches } : undefined,
  out_of_scope_policy: () => ({ headline: "org outside the policy's scope", lines: [] }),
  out_of_scope_rule: () => ({ headline: "org outside the rule's scope", lines: [] }),
  empty_repository: () => ({ headline: "empty repository", lines: [] }),
  foreign_pr_open: () => ({ headline: "yielding to an open pull request", lines: [] }),
  ignored_global: (e) => (str(e.pattern) ? { headline: "ignored by the policy", lines: [`pattern ${e.pattern}`] } : undefined),
  ignored_rule: (e) => (str(e.pattern) ? { headline: "ignored by the rule", lines: [`pattern ${e.pattern}`] } : undefined),
  gate_closed: (e) => (str(e.referee) ? { headline: `gate closed: ${e.referee} not satisfied`, lines: [] } : undefined),
  branch_missing: (e) => (str(e.branch) ? { headline: `branch ${e.branch} does not exist`, lines: [] } : undefined),
  gate_error: (e) => (str(e.referee) && str(e.error) ? { headline: `gate error evaluating ${e.referee}`, lines: [e.error] } : undefined),
  migrated_from_v1: () => ({ headline: "last checked by v1; details on next check", lines: [] }),
};

// describeEvidence renders a finding's reason and evidence. A reason
// with no renderer, or evidence in a shape the renderer does not know,
// shows only the reason code.
export function describeEvidence(f: Finding): EvidenceView | undefined {
  const reason = f.reason;
  if (!reason) {
    return undefined;
  }
  const render = Object.hasOwn(renderers, reason) ? renderers[reason] : undefined;
  const fields = (f.evidence ?? { reason }) as unknown as Fields;
  const view = render && fields.reason === reason ? render(fields) : undefined;
  return view ? { reason, ...view } : { reason, headline: reason, lines: [] };
}
