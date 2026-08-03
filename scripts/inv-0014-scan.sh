#!/usr/bin/env bash
# inv-0014-scan.sh - Measure the blast radius of the INV-0014 orphan-deletion bug.
#
# INV-0014: discoverOrphans misclassifies a file that exists on the default
# branch as an orphan repo-guardian authored, and commits a deletion of it to
# the reconcile branch of an open PR. Merging such a PR removes a legitimate
# file from the default branch.
#
# This script is STRICTLY READ-ONLY. It performs no writes, no merges, no
# closes, and no branch deletions. It answers three questions per affected
# repository:
#
#   1. Does a repo-guardian PR carry a `chore: remove ...` orphan commit?
#   2. Was that PR merged, closed, or is it still open?
#   3. Does the deleted path exist on the default branch RIGHT NOW?
#
# Question 3 is the one that matters. A missing file with a merged PR is
# confirmed data loss and needs restoring; a missing file with an unmerged PR
# means something else removed it; an open PR with the file still present is
# containment — close the PR before anyone merges it.
#
# Usage:
#   ./scripts/inv-0014-scan.sh ORG [ORG...]
#
# Requires: gh (authenticated), jq
#
# Output: TSV on stdout (repo, pr, pr_state, path, on_default, verdict) plus a
# human summary on stderr, so `./scripts/inv-0014-scan.sh myorg > findings.tsv`
# gives a clean file to work from.

set -euo pipefail

readonly BRANCH="repo-guardian/add-missing-files"
# Matches the commit message built in internal/checker/drift.go cleanupOrphans.
readonly ORPHAN_MSG_RE='^chore: remove (.+) \(rule "(.+)" satisfied on default branch\)$'

RED='\033[0;31m'
YELLOW='\033[0;33m'
GREEN='\033[0;32m'
NC='\033[0m'
readonly RED YELLOW GREEN NC

die() {
  printf '%berror:%b %s\n' "$RED" "$NC" "$1" >&2
  exit 1
}

note() {
  printf '%s\n' "$1" >&2
}

usage() {
  sed -n '2,30p' "$0" | sed 's/^# \{0,1\}//'
  exit "${1:-0}"
}

check_deps() {
  command -v gh >/dev/null 2>&1 || die "gh is not installed"
  command -v jq >/dev/null 2>&1 || die "jq is not installed"
  gh auth status >/dev/null 2>&1 || die "gh is not authenticated; run: gh auth login"
}

# find_prs lists "owner/repo<TAB>number" for every PR in the org whose head
# branch is the reconcile branch, in any state. Search is capped at 1000
# results; the count is echoed so a truncated fleet is visible rather than
# silently partial.
find_prs() {
  local org=$1
  gh search prs "head:${BRANCH}" --owner "$org" --limit 1000 \
    --json repository,number \
    --jq '.[] | "\(.repository.nameWithOwner)\t\(.number)"'
}

# pr_state returns "merged", "closed", or "open".
pr_state() {
  local repo=$1 num=$2
  gh api "repos/${repo}/pulls/${num}" \
    --jq 'if .merged then "merged" else .state end'
}

# orphan_paths prints every path removed by an orphan-cleanup commit on the
# given PR, one per line. A PR with no such commit prints nothing.
orphan_paths() {
  local repo=$1 num=$2
  gh api "repos/${repo}/pulls/${num}/commits" --paginate --jq '.[].commit.message' |
    # Commit messages are multi-line; only the subject can match.
    head -c 100000 |
    sed -nE "s/${ORPHAN_MSG_RE}/\1/p"
}

# path_on_default is "yes" if the path exists on the repo's default branch
# today, "no" if GitHub returns 404, "unknown" on any other failure. Unknown is
# never treated as safe.
path_on_default() {
  local repo=$1 path=$2 status
  status=$(gh api "repos/${repo}/contents/${path}" \
    --silent --include 2>/dev/null | head -1 | grep -oE '[0-9]{3}' | head -1 || true)

  case "$status" in
    200) printf 'yes' ;;
    404) printf 'no' ;;
    *) printf 'unknown' ;;
  esac
}

# verdict maps (pr_state, on_default) to the operator action.
verdict() {
  local state=$1 on_default=$2

  case "${state}:${on_default}" in
    merged:no) printf 'DATA LOSS - restore from git history' ;;
    merged:yes) printf 'merged but file present - verify manually' ;;
    open:yes) printf 'CONTAINMENT - close PR before it is merged' ;;
    open:no) printf 'file already absent - PR would re-delete nothing' ;;
    closed:no) printf 'file absent, PR not merged - another cause' ;;
    closed:yes) printf 'no action - PR closed unmerged' ;;
    *:unknown) printf 'INDETERMINATE - probe failed, check by hand' ;;
    *) printf 'review' ;;
  esac
}

scan_pr() {
  local repo=$1 num=$2 state path on_default v
  local -i found=0

  state=$(pr_state "$repo" "$num")

  while IFS= read -r path; do
    [[ -n "$path" ]] || continue
    found=1
    on_default=$(path_on_default "$repo" "$path")
    v=$(verdict "$state" "$on_default")
    printf '%s\t%s\t%s\t%s\t%s\t%s\n' "$repo" "$num" "$state" "$path" "$on_default" "$v"

    case "$v" in
      DATA*) note "$(printf '  %b%s%b #%s %s (%s)' "$RED" "$repo" "$NC" "$num" "$path" "$v")" ;;
      CONTAINMENT*) note "$(printf '  %b%s%b #%s %s (%s)' "$YELLOW" "$repo" "$NC" "$num" "$path" "$v")" ;;
      *) note "$(printf '  %s #%s %s (%s)' "$repo" "$num" "$path" "$v")" ;;
    esac
  done < <(orphan_paths "$repo" "$num")

  return $((found == 0))
}

main() {
  [[ $# -gt 0 ]] || usage 1
  [[ "${1:-}" != "--help" && "${1:-}" != "-h" ]] || usage 0

  check_deps

  printf 'repo\tpr\tpr_state\tpath\ton_default\tverdict\n'

  local -i scanned=0 affected=0

  for org in "$@"; do
    note "scanning ${org} for PRs with head ${BRANCH} ..."

    while IFS=$'\t' read -r repo num; do
      [[ -n "$repo" ]] || continue
      scanned+=1

      if scan_pr "$repo" "$num"; then
        affected+=1
      fi
    done < <(find_prs "$org")
  done

  note ''
  note "$(printf '%bscanned %d repo-guardian PRs; %d carry an orphan-deletion commit%b' \
    "$GREEN" "$scanned" "$affected" "$NC")"
  note 'Rows marked DATA LOSS or CONTAINMENT need action. This script changed nothing.'
}

main "$@"
