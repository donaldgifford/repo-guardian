import type { Schemas } from "../api/client";

// prLink builds a PR's URL from structured fields — host, org, name and
// number — never from a URL string in the evidence, so a crafted value
// cannot point the link anywhere else. It returns undefined when a part
// does not look like what GitHub would issue.
export function prLink(host: string, org: string, repo: string, number: number): string | undefined {
  const name = /^[A-Za-z0-9_.-]+$/;
  if (!/^[A-Za-z0-9.-]+(:\d+)?$/.test(host) || !name.test(org) || !name.test(repo) || !Number.isSafeInteger(number) || number <= 0) {
    return undefined;
  }
  return `https://${host}/${encodeURIComponent(org)}/${encodeURIComponent(repo)}/pull/${number}`;
}

// repoLink is the repository's page on the host, built the same way.
export function repoLink(host: string, org: string, repo: string): string | undefined {
  const name = /^[A-Za-z0-9_.-]+$/;
  if (!/^[A-Za-z0-9.-]+(:\d+)?$/.test(host) || !name.test(org) || !name.test(repo)) {
    return undefined;
  }
  return `https://${host}/${encodeURIComponent(org)}/${encodeURIComponent(repo)}`;
}

// prLabel describes a finding's PR: "PR #412 (9d)" for repo-guardian's,
// "PR #88 (human)" for one the rule yields to.
export function prLabel(f: Pick<Schemas["Finding"], "pr" | "remediation">, now: Date = new Date()): string | undefined {
  if (!f.pr) {
    return undefined;
  }
  if (f.remediation === "foreign_pr") {
    return `PR #${f.pr.number} (human)`;
  }
  if (f.pr.created_at) {
    const days = Math.floor((now.getTime() - new Date(f.pr.created_at).getTime()) / 86_400_000);
    return `PR #${f.pr.number} (${days}d)`;
  }
  return `PR #${f.pr.number}`;
}
