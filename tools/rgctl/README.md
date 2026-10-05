# rgctl

`rgctl` inspects a repo-guardian v1 policy file and finds or closes the pull
requests repo-guardian opened. It is its own Go module: its `go.mod` never
requires the product module, so it keeps building after v1's packages are gone
(DESIGN-0035).

## Install

```bash
go install github.com/donaldgifford/repo-guardian/tools/rgctl@main
```

## Commands

```bash
rgctl config show guardian.hcl                       # what a v1 policy declares
rgctl prs list --org acme --format json --out acme.json   # find open repo-guardian PRs
rgctl prs close --from acme.json --yes                # close exactly the recorded PRs
rgctl version
```

Nothing is written without `--yes`.
