import { expect, test } from "bun:test";

import { toneFor } from "./format";
import { BRANCH_HOLDS, HOLD_SENTENCES, HOLDS, holdSentence } from "./holds";

test("every hold has a sentence and a tone; an unknown hold is its code", () => {
  expect(HOLDS).toHaveLength(5);
  for (const h of HOLDS) {
    expect(HOLD_SENTENCES[h]).toBeTruthy();
    expect(holdSentence(h)).toBe(HOLD_SENTENCES[h]!);
    expect(holdSentence(h)).not.toBe(h);
    expect(toneFor("hold", h)).not.toBe("mute" as never);
  }
  expect(holdSentence("teleported")).toBe("teleported");
  expect(toneFor("hold", "teleported")).toBe("mute");
});

test("the sentences are DESIGN-0037's", () => {
  expect(holdSentence("pr_cap")).toBe("the repository is at max_open_prs; waits for a slot");
  expect(holdSentence("foreign_branch")).toBe("a repo-guardian/ branch exists that the bot did not create; rename or delete it to resume");
  expect(holdSentence("stale_branch")).toBe("branch left from a closed PR; delete it to resume");
  expect(holdSentence("conflict")).toBe("the PR branch conflicts with the default branch and could not be updated");
  expect(holdSentence("permission")).toBe("the Remediation App lacks permission for this change");
});

test("the branch holds are the four that precede a pull request", () => {
  expect([...BRANCH_HOLDS]).toEqual(["pr_cap", "foreign_branch", "stale_branch", "conflict"]);
  expect(HOLDS.filter((h) => !(BRANCH_HOLDS as readonly string[]).includes(h))).toEqual(["permission"]);
});
