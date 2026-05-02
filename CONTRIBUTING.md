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

## Commit messages

Follow [Conventional Commits](https://www.conventionalcommits.org/).
The first line is `<type>(<scope>): <subject>`, where `<type>` is
one of `feat`, `fix`, `chore`, `docs`, `refactor`, `test`, `perf`,
`style`, `ci`, `build`, or `revert`. The scope is optional.

git-cliff parses these for changelog generation, so non-conventional
commits get bucketed into "Other."

## Releasing

### Binary releases

Triggered automatically on merge to `main` when a PR carries a
`major`, `minor`, or `patch` label. The release workflow bumps the
tag, builds binaries via goreleaser (CGO disabled), and pushes
artifacts. GPG-signed.

### Helm chart releases

Independent cadence from binary releases. Bump
`charts/repo-guardian/Chart.yaml` `version` and (if needed)
`appVersion`, then merge to `main`. The chart-release workflow
publishes to `oci://ghcr.io/donaldgifford/charts/repo-guardian`
with cosign keyless signing and SLSA provenance.

The chart `CHANGELOG.md` is regenerated on-the-fly by the publish
workflow and packaged inside the `.tgz`. Do not edit it by hand.

### Root CHANGELOG

Regenerated automatically when a binary release tag (`v*`) is pushed
to the repo. The `changelog-update` workflow runs `git-cliff`
against `cliff.toml` and opens a PR titled
`docs(changelog): update root CHANGELOG for <tag>`. Merging the PR
keeps the root `CHANGELOG.md` current.

## Local development

See `CLAUDE.md` for the project's development guidelines, build
commands, and architecture overview.
