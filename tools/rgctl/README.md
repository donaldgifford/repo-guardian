# rgctl

`rgctl` inspects a repo-guardian v1 policy file and finds or closes the pull
requests repo-guardian opened. Operator documentation is
[docs/operations/rgctl.md](../../docs/operations/rgctl.md); the design is
DESIGN-0035 and the plan IMPL-0027.

## Install

```bash
go install github.com/donaldgifford/repo-guardian/tools/rgctl@main
```

## Commands

```bash
rgctl config show guardian.hcl                            # what a v1 policy declares
rgctl prs list --org acme --format json --out acme.json   # find open repo-guardian PRs
rgctl prs close --from acme.json                          # dry run: print the plan
rgctl prs close --from acme.json --yes                    # comment, then close
rgctl version
```

Nothing is written without `--yes`.

## Module boundary

This directory is its own Go module (`github.com/donaldgifford/repo-guardian/tools/rgctl`).
Its `go.mod` never requires or replaces the product module, so nothing under the
product's `internal/` can be imported and the tool outlives v1's packages. Two
checks hold the line: `make lint-rgctl` fails on a `require` or `replace` naming
the parent, and a depguard rule in the root `.golangci.yml` denies the product's
packages for files under `tools/rgctl`.

The product's go-github is v68; this module uses v75, the version ghinstallation
v2.17.0 requires.

## Layout

| Package | Role |
| ------- | ---- |
| `.` (main) | cobra commands, exit codes, output streams |
| `internal/clog` | `log/slog` logger over charmbracelet/log |
| `internal/v1config` | loose v1 `guardian.hcl` reader, never evaluates |
| `internal/ghapi` | the only GitHub client: App or token auth, search pacing, one retry on rate limits |
| `internal/ghapi/ghapitest` | stateful `httptest` fake of every endpoint rgctl calls |
| `internal/prs` | identity rule, classification, the JSON record, list and close |

## Development

From the repository root:

```bash
make build-rgctl         # build/bin/rgctl
make lint-rgctl          # golangci-lint with the root config, plus the go.mod boundary check
make test-rgctl          # go test -race ./... inside the module
make test-coverage-rgctl # coverage-rgctl.out, uploaded to Codecov under the rgctl flag
```

`make lint`, `make test`, `make test-coverage`, `make build` and `make ci` all
include these. Goldens regenerate with `go test ./... -update` from
`tools/rgctl`.
