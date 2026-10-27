// Hold reasons are sentences, not codes (DESIGN-0037 §Remediations
// view): each says what an operator does about it.
export const HOLD_SENTENCES: Record<string, string> = {
  pr_cap: "the repository is at max_open_prs; waits for a slot",
  foreign_branch: "a repo-guardian/ branch exists that the bot did not create; rename or delete it to resume",
  stale_branch: "branch left from a closed PR; delete it to resume",
  conflict: "the PR branch conflicts with the default branch and could not be updated",
  permission: "the Remediation App lacks permission for this change",
};

export const HOLDS = ["pr_cap", "foreign_branch", "stale_branch", "conflict", "permission"] as const;

// BRANCH_HOLDS live on control_results before any pull request exists;
// /remediations lists them as held results only under a hold filter.
export const BRANCH_HOLDS = ["pr_cap", "foreign_branch", "stale_branch", "conflict"] as const;

// holdSentence is the sentence for a hold; an unknown hold renders its
// code, never blank.
export function holdSentence(hold: string): string {
  return HOLD_SENTENCES[hold] ?? hold;
}
