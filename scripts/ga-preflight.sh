#!/usr/bin/env bash
#
# GA preflight (IMPL-0034 6.3, OQ6). The maintainer runs it on the head of
# the v2 → main pull request (M2) before merging it. Every check runs; the
# script exits non-zero naming each one that failed.
#
# Usage: scripts/ga-preflight.sh [--pr NUMBER | --skip-label]
#   --pr NUMBER   the M2 pull request, whose labels must be exactly `major`
#   --skip-label  skip the label check (before the M2 pull request exists)

set -uo pipefail

readonly VERSION="2.0.0"
readonly LAST_RC="v2.0.0-rc.17"
readonly CHART="charts/repo-guardian/Chart.yaml"

pr=""
skip_label=false
failed=()

usage() {
  echo "usage: $0 [--pr NUMBER | --skip-label]" >&2
  exit 2
}

while (($# > 0)); do
  case "$1" in
    --pr)
      [[ $# -ge 2 ]] || usage
      pr="$2"
      shift 2
      ;;
    --skip-label)
      skip_label=true
      shift
      ;;
    *) usage ;;
  esac
done

if [[ -z "${pr}" && "${skip_label}" == false ]]; then
  usage
fi

# check NAME COMMAND...: runs COMMAND quietly, records NAME on failure.
check() {
  local name="$1"
  shift
  if "$@" >/dev/null 2>&1; then
    echo "ok    ${name}"
  else
    echo "FAIL  ${name}"
    failed+=("${name}")
  fi
}

main_is_ancestor() {
  git fetch --quiet origin main --tags &&
    git merge-base --is-ancestor origin/main HEAD
}

last_rc_reachable() {
  git tag --merged HEAD | grep -qx "${LAST_RC}"
}

no_ga_tag_or_release() {
  ! git ls-remote --exit-code --tags origin "refs/tags/v${VERSION}" &&
    ! gh release view "v${VERSION}"
}

bump_reads_main_only() {
  [[ "$(grep -c 'base-branch: true' .github/workflows/release.yml)" == 1 ]]
}

chart_is_ga() {
  [[ "$(yq '.version' "${CHART}")" == "${VERSION}" ]] &&
    [[ "$(yq '.appVersion' "${CHART}")" == "${VERSION}" ]]
}

docs_sweep_clean() {
  # The IMPL-0034 3.10 grep. Every hit must be a removal sentence, a
  # v1-to-v2 comparison or under a history admonition; the allowlist
  # below is that reviewed set, so a new hit fails.
  local hits
  hits="$(grep -rln 'guardian\.hcl\|watch = true\|Valkey\|staleSweep\|STORE_BACKEND' \
    README.md docs/index.md docs/usage \
    docs/operations/{aws,scaling,ent-setup,v2-*}.md \
    charts/repo-guardian/README.md.gotmpl |
    grep -vx -e README.md -e docs/usage/policy-reference.md \
      -e docs/operations/scaling.md -e docs/operations/aws.md \
      -e 'docs/operations/v2-[a-z]*\.md' \
      -e charts/repo-guardian/README.md.gotmpl)"
  [[ -z "${hits}" ]]
}

pr_label_is_major() {
  [[ "$(gh pr view "${pr}" --json labels --jq '[.labels[].name] | sort | join(",")')" == "major" ]]
}

check "origin/main is an ancestor of HEAD" main_is_ancestor
check "${LAST_RC} is reachable from HEAD" last_rc_reachable
check "no v${VERSION} tag or release exists" no_ga_tag_or_release
check "release.yml bumps from main's tags only" bump_reads_main_only
check "${CHART} version and appVersion are ${VERSION}" chart_is_ga
check "make lint-ui" make lint-ui
check "make lint-docs" make lint-docs
check "docs sweep (IMPL-0034 3.10) is clean" docs_sweep_clean
if [[ "${skip_label}" == true ]]; then
  echo "skip  pull request label (--skip-label)"
else
  check "PR #${pr} carries exactly the major label" pr_label_is_major
fi

if ((${#failed[@]} > 0)); then
  echo
  echo "preflight failed: ${#failed[@]} check(s):"
  printf '  - %s\n' "${failed[@]}"
  exit 1
fi
echo
echo "preflight passed"
