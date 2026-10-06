# rgctl: find and close repo-guardian pull requests

`rgctl` is a small command-line tool for operators. It reads a repo-guardian
v1 `guardian.hcl` without the v1 loader, finds the pull requests repo-guardian
opened, writes them to a JSON record, and closes exactly the recorded ones with
a pointer comment. Use it to troubleshoot v1, to turn v1 off in an org, and at
the v2 cutover. The design is
[DESIGN-0035](../design/0035-rgctl-a-standalone-cli-to-inspect-v1-policy-and-find-or-close.md).

It lives in `tools/rgctl` as its own Go module. That module never requires the
product module, so the tool keeps building after v1's packages are deleted.

## Install

```bash
go install github.com/donaldgifford/repo-guardian/tools/rgctl@main
rgctl version
```

From a checkout, `make build-rgctl` writes `build/bin/rgctl`.

## Credentials

Pick one shape. Neither is ever passed as a flag value.

| Shape | Set | Notes |
| ----- | --- | ----- |
| GitHub App | `--app-id` or `RGCTL_APP_ID`, and `--private-key-file` or `RGCTL_PRIVATE_KEY_FILE` | The bot login is discovered from `GET /app`. Each org gets its own installation token, so rgctl sees exactly the repositories v1 could see. Needed for a legacy-mode `--config`. |
| Operator token | `GH_TOKEN` in the environment, and `--bot-login` or `RGCTL_BOT_LOGIN` | `--bot-login` is the App's account, `<app slug>[bot]`. Comments and closes are attributed to the token's owner. |

When both are set, the App wins and rgctl logs a warning. For GitHub
Enterprise Server, set `--github-host` or `RGCTL_GITHUB_HOST` to the host or
its URL.

### App setup

Use the repo-guardian App itself: rgctl must see the same installations v1
does, and the bot login it matches on is that App's. Its existing permissions
already cover everything rgctl does. A separate read-only App for auditing
needs only the `prs list` column.

| Repository permission | `prs list` | `prs close` | Used for |
| --------------------- | ---------- | ----------- | -------- |
| Metadata | read | read | installation repositories, search |
| Pull requests | read | write | search, read PRs, commits and comments; post the comment and close |
| Contents | none | write | check the head branch exists and delete it (`--delete-branch` only) |

Keep the private key outside the repository and readable only by you:

```bash
mkdir -p ~/.config/rgctl
mv ~/Downloads/<app>.private-key.pem ~/.config/rgctl/app.pem
chmod 600 ~/.config/rgctl/app.pem
export RGCTL_APP_ID=<app id>
export RGCTL_PRIVATE_KEY_FILE=~/.config/rgctl/app.pem
```

With a token, `gh auth token` carries the `repo` scope, which covers the same
calls.

## Commands

### `rgctl config show <guardian.hcl>`

Prints what a v1 policy declares: the orgs in `scope` (or legacy mode, every
installed org), the `guardian {}` knobs, every rule with its paths, check mode,
scope, ignores, gate and reconcilers, every `pr` block, global ignores, and
anything rgctl did not recognise with its line number. Values are printed as
written and never evaluated. `--format json` emits the same as JSON. It needs
no credentials.

### `rgctl prs list`

Finds open repo-guardian pull requests. Choose exactly one selection:

| Flag | Selects |
| ---- | ------- |
| `--repo org/name` | one repository |
| `--org org` | one org, searched then verified |
| `--config guardian.hcl` | every org in the file's `scope`; in legacy mode, every App installation |

A pull request is repo-guardian's when it is open, the App's bot authored it,
its head branch starts with `repo-guardian/`, and its head is not a fork.
`--branch` (repeatable) narrows that to exact branch names. Org scans use
search, then read every hit back through the Pull Requests API, so a stale
search result is dropped rather than acted on. When search reports incomplete
results or hits its 1000-result cap, rgctl warns once; rerun with
`--exhaustive`, which reads every repository's open pull requests instead.

Each pull request is marked `edited` when any commit on it is by someone other
than the App. The committer `web-flow` does not count: GitHub records it on
every commit made through the API, the App's included. A person editing in the
web UI is still the commit's author, so their edit is still caught.

Output is a table by default. `--format json` writes the record to stdout,
and `--out <path>` writes it to a file as well.

### `rgctl prs close`

Closes pull requests: posts a pointer comment, closes, and with
`--delete-branch` deletes the head branch. Input is a record (`--from
record.json`) or the same selection flags as `list`, not both: `--from`
already names the PRs, so it is never combined with `--repo`, `--org` or
`--config` (exit 2).

To close a subset, cut the record down with `jq` first:

```bash
jq '.prs |= map(select(.repository == "acme/web" and .number == 12))' acme.json > one.json
rgctl prs close --from one.json                          # plan
rgctl prs close --from one.json --yes --delete-branch    # act
```

- **Dry run by default.** Without `--yes` it prints the plan and writes
  nothing.
- **Every PR is re-read first.** A PR closed in the meantime is reported as
  `already_closed`. One that no longer matches the identity rule is
  `skipped_not_ours`.
- **Edited PRs are skipped** unless `--force`. Their branch is never deleted,
  even with `--force`.
- **Order is comment, close, branch,** so a failure part-way leaves a PR that
  explains itself. Re-running is safe: the comment's first line is the marker
  `<!-- rgctl:closed-by-migration:v1 -->` and is never posted twice.
- `--comment <text>` replaces the default pointer text. The marker line stays.

The result record adds a `result` per PR: `planned`, `closed`,
`branch_deleted`, `already_closed`, `skipped_edited`, `skipped_not_ours` or
`error` with a message.

## The cutover shape

The record from the list is the only input to the close, so the close acts on
exact identities rather than on a fresh search.

```bash
export GH_TOKEN=...            # or RGCTL_APP_ID + RGCTL_PRIVATE_KEY_FILE
export RGCTL_BOT_LOGIN='repo-guardian[bot]'

rgctl prs list --org acme --exhaustive --format json --out acme.json
jq '.summary, [.prs[] | select(.edited)]' acme.json    # review
rgctl prs close --from acme.json                        # dry run: read the plan
rgctl prs close --from acme.json --yes --delete-branch --out acme-closed.json
```

Edited PRs stay open. At the v2 cutover they are left for the controls engine
to adopt as tracked PRs.

## Exit codes

| Code | `list` | `close` |
| ---- | ------ | ------- |
| 0 | none found | every planned action done, or nothing to do |
| 1 | repo-guardian PRs found | PRs skipped as edited |
| 2 | usage or configuration error | usage or configuration error |
| 3 | an org or repository failed | a PR failed |

A runbook can gate on `rgctl prs list --org acme; test $? -eq 0`.

## Output streams

The log is human-readable, levelled and coloured on a terminal
(`--log-level`, `--no-color`, `NO_COLOR`). It goes to stdout beside the table.
When `--format json` writes the record to stdout, the log moves to stderr for
that run, so `rgctl prs list --org acme --format json | jq .summary` works.

## Limits

- Search allows 30 requests a minute; rgctl spaces search calls two seconds
  apart. A rate limit is retried once after GitHub's reset, then reported with
  exit 3.
- Search returns at most 1000 results per query. Use `--exhaustive` for a
  complete list.
- `--exhaustive` with a token lists repositories through the organization
  endpoint, so it fails for a personal account. Use App credentials there;
  search mode works for both.
