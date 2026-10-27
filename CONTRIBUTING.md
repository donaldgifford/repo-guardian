# Contributing

## Branches and PRs

Use the `git-workflow` skill (`/branch <type> <description>`) to create
branches with the correct type prefix:

- `feat/` for new features
- `fix/` for bug fixes
- `chore/` for maintenance, dependency bumps, tooling
- `docs/` for documentation-only changes
- `bug/` for bug investigations and fixes

Every PR must carry exactly one semver label — `major`, `minor`,
`patch`, or `dont-release`. The release workflow uses these to
auto-tag the binary release on merge to `main`.

A PR that bumps the chart's `appVersion` (a release or an rc) also adds
an entry at the top of `ui/CHANGELOG.yaml` and runs
`cd ui && bun run gen:changelog`; `make lint-ui` fails when the newest
entry is not the chart's `appVersion`. The status page renders the
newest five entries as its Changelog card.

## Commit messages

Follow [Conventional Commits](https://www.conventionalcommits.org/).
The first line is `<type>(<scope>): <subject>`, where `<type>` is
one of `feat`, `fix`, `chore`, `docs`, `refactor`, `test`, `perf`,
`style`, `ci`, `build`, or `revert`. The scope is optional.

git-cliff parses these for changelog generation, so non-conventional
commits get bucketed into "Other."

## Releasing

### Binary releases

A GA release is cut by its semver label, never by a hand tag. Merging
a PR into `main` labelled `major`, `minor` or `patch` runs
`release.yml`: it tags the merge commit with the next version (only
tags reachable from `main` count), builds GPG-signed binaries via
goreleaser (CGO disabled) with an SPDX SBOM per archive, and publishes
the images and chart through `ghcr.yml` (and `ecr.yml` when enabled).
Check which tag a label will produce before applying it.

### Release candidates

`release.yml` cannot cut pre-releases, so an rc is cut by hand:

1. Open a PR labelled `dont-release` that bumps the chart to the rc
   (`version` and `appVersion`, for example `2.1.0-rc.1`), adds the
   `ui/CHANGELOG.yaml` entry and runs `cd ui && bun run gen:changelog`.
2. After it merges, push a signed tag on the merge commit:
   `git tag -s v2.1.0-rc.1 <sha> -m v2.1.0-rc.1 && git push origin v2.1.0-rc.1`.
   `prerelease.yml` builds the GitHub pre-release from the tag.
3. Publish the images and chart:
   `gh workflow run ghcr.yml --ref v2.1.0-rc.1 -f tag=v2.1.0-rc.1`.

A tag with a `-` suffix never moves `latest`.

### Helm chart releases

Independent cadence from binary releases. Bump
`charts/repo-guardian/Chart.yaml` `version` and (if needed)
`appVersion`, then merge to `main`. The chart-release workflow
publishes to `oci://ghcr.io/donaldgifford/charts/repo-guardian`
with cosign keyless signing and SLSA provenance.

The chart `CHANGELOG.md` is regenerated on-the-fly by the publish
workflow and packaged inside the `.tgz`. Do not edit it by hand.

### Root CHANGELOG

After each GA release, dispatch the `changelog-update` workflow with
the tag (`gh workflow run changelog-update.yml -f tag=vX.Y.Z`). The tag
`release.yml` pushes uses `GITHUB_TOKEN`, so it starts no workflow by
itself. The workflow runs `git-cliff --ignore-tags '.*-.*' --prepend
CHANGELOG.md <previous GA tag>..<tag>` against `cliff.toml`: the new
section covers the whole release line including its rcs, sits above
the existing sections and leaves them untouched. It opens a
`dont-release` PR titled `docs(changelog): update root CHANGELOG for
<tag>`. If the repository does not allow Actions to create pull
requests, it pushes the `chore/changelog-<tag>` branch and you open
the PR from it.

## Local development

See `CLAUDE.md` for the project's development guidelines, build
commands, and architecture overview.
