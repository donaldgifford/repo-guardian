---
id: IMPL-0033
title: "Identity-provider portability for the API and UI (DESIGN-0038 Part B)"
status: Draft
author: Donald Gifford
created: 2026-10-25
---

<!-- markdownlint-disable-file MD024 MD025 MD041 -->

# IMPL-0033: Identity-provider portability for the API and UI (DESIGN-0038 Part B)

<!--toc:start-->
<!-- markdownlint-capture -->
<!-- markdownlint-disable MD013 -->
- [Objective](#objective)
- [Scope](#scope)
  - [In Scope](#in-scope)
  - [Out of Scope](#out-of-scope)
- [Pre-implementation audit](#pre-implementation-audit)
- [How the phases are ordered](#how-the-phases-are-ordered)
- [Implementation Phases](#implementation-phases)
  - [Phase 1: The API's client claim](#phase-1-the-apis-client-claim)
    - [Tasks](#tasks)
    - [Success Criteria](#success-criteria)
  - [Phase 2: The BFF's token endpoint method and authorization parameters](#phase-2-the-bffs-token-endpoint-method-and-authorization-parameters)
    - [Tasks](#tasks-1)
    - [Success Criteria](#success-criteria-1)
  - [Phase 3: Chart values and schema](#phase-3-chart-values-and-schema)
    - [Tasks](#tasks-2)
    - [Success Criteria](#success-criteria-2)
  - [Phase 4: The identity-provider page and the recipes](#phase-4-the-identity-provider-page-and-the-recipes)
    - [Tasks](#tasks-3)
    - [Success Criteria](#success-criteria-3)
  - [Phase 5: rc.17](#phase-5-rc17)
    - [Tasks](#tasks-4)
    - [Success Criteria](#success-criteria-4)
- [File Changes](#file-changes)
- [Testing Plan](#testing-plan)
- [Dependencies](#dependencies)
- [Open Questions](#open-questions)
- [References](#references)
<!-- markdownlint-restore -->
<!--toc:end-->

<!--docz:objective:start-->
## Objective

Make the API and UI run against Okta, Entra ID or Auth0 as readily as
against Keycloak, by making the three provider assumptions in the code
and chart configurable and by documenting the identity-provider
contract with a recipe per provider. Ships as `v2.0.0-rc.17`, before
the GA cutover (DESIGN-0038 OQ1).

**Implements:** [DESIGN-0038](../design/0038-v200-ga-cutover-and-identity-provider-portability.md)
Part B (B1–B6), decisions D4–D7, OQ4–OQ9.

<!--docz:objective:end-->

<!--docz:scope:start-->
## Scope

<!--docz:in-scope:start-->
### In Scope

- `OIDC_CLIENT_CLAIM` on the api role (default `azp`), read where
  `Claims.Client` is set; the typed `AZP` field goes.
- `OIDC_TOKEN_AUTH_METHOD` (`client_secret_post` default,
  `client_secret_basic`) and `OIDC_AUTHORIZATION_PARAMS` (JSON object,
  reserved keys refused) on the BFF.
- Chart: `api.auth.clientClaim`, `ui.oidc.tokenAuthMethod`,
  `ui.oidc.authorizationParams`; `nameClaim`/`groupsClaim` in the
  schema; `ui.oidc.scopes` default aligned with the BFF's.
- Test fixtures: Okta-, Entra- and namespaced-claim-shaped tokens in
  `oidctest`; the mock issuer accepting `client_secret_basic`; one e2e
  project on the basic method.
- `docs/operations/identity-provider.md`: the contract table, the
  Keycloak section moved from `docs/usage/api.md`, Okta, Entra ID and
  Auth0 recipes, the Google note, verification through `/me`.
- The rc.17 release bump, tag and publish.

<!--docz:in-scope:end-->

<!--docz:out-of-scope:start-->
### Out of Scope

- Nested group-claim paths (OQ9 decided no).
- Any change to the authorization model, `/me`, or the OpenAPI
  document.
- ID-token acceptance at the API, opaque-token introspection, Google
  without a broker.
- Homelab Keycloak reconfiguration: the defaults keep today's
  behaviour, so the rc.17 upgrade needs no IdP change.

<!--docz:out-of-scope:end-->
<!--docz:scope:end-->

## Pre-implementation audit

Verified against the tree on 2026-10-25 (DESIGN-0038 § How the API and
UI authenticate today):

- `internal/api/authn.go`: `AuthnConfig{Issuer, Audience, NameClaim,
  GroupsClaim, Now}`; `tokenClaims.AZP` is the only source of
  `Claims.Client`; `verify` already reads name and groups by configured
  key from the full claim map, so the client claim follows the same
  path. `isIDToken` names Keycloak in its comment.
- `internal/config/api.go` reads `OIDC_NAME_CLAIM` and
  `OIDC_GROUPS_CLAIM` with defaults; `cmd/repo-guardian/api.go` builds
  `AuthnConfig` from them.
- `internal/api/oidctest/oidctest.go` `Token` has `Client` (→ `azp`),
  `Groups`, `Name` (→ `preferred_username`), `IDToken`, `Rogue`,
  `HMAC`; the header `typ` is `at+jwt`. `NameClaim: "preferred_username",
  GroupsClaim: "groups"` is hard-coded in `api_test.go`,
  `status_http_test.go` and `apiendpoints_integration_test.go`.
- `ui/server/oidc.ts` passes `client.ClientSecretPost`;
  `ui/server/auth.ts` builds the authorize URL from six fixed
  parameters; `ui/server/config.ts` has `defaultScopes = "openid profile
  email offline_access"` and no audience or method setting.
- `ui/server/testing/mock-issuer.ts` checks the client secret from the
  POST body only; `ui/e2e/stack.ts` starts two BFFs (`startUI`).
- Chart: `values.yaml` `api.auth.{issuer,audience,nameClaim,groupsClaim}`
  and `ui.oidc.{clientId,scopes: [openid, profile, groups,
  offline_access]}`; `values.schema.json` declares only `enabled`,
  `issuer`, `audience` under `api.auth`; `_helpers.tpl` renders the
  `OIDC_*` env; `validateUI` holds the UI render guards;
  `tests/ui_test.yaml` asserts the scopes default string.
- Docs: `docs/usage/api.md` § Keycloak example; `docs/usage/ui.md`
  links it; `ui/README.md` has the env table.

## How the phases are ordered

API first (Phase 1) because its fixtures are what Phases 2–4 are tested
against; the BFF (Phase 2) next because the chart (Phase 3) renders its
new env; docs (Phase 4) once every knob has its final name; the rc
(Phase 5) last. Phases 1–4 are one pull request against `v2`
([[feedback-one-pr-per-impl]] in memory); Phase 5 is the release-bump
pull request.

## Implementation Phases

Each phase builds on the previous one. A phase is complete when all its
tasks are checked off and its success criteria are met.

---

<!--docz:phase:start-->
### Phase 1: The API's client claim

DESIGN-0038 B3.1, B3.2, B3.6; D5; OQ4.

<!--docz:tasks:start-->
#### Tasks

- [x] 1.1 **Config.** `internal/config/api.go`: `APIConfig.OIDCClientClaim`
  from `OIDC_CLIENT_CLAIM` (`envOrDefault`, default `azp`); validation
  rejects an empty string when auth is enabled (an operator who sets it
  blank gets a startup error naming the variable, not a silent "no
  client"). Table case in `internal/config/role_test.go`.
- [x] 1.2 **AuthnConfig.** `internal/api/authn.go`: `AuthnConfig.ClientClaim`;
  `verify` sets `Client: stringClaim(all[a.cfg.ClientClaim])`; remove
  `tokenClaims.AZP`. `cmd/repo-guardian/api.go` passes
  `ClientClaim: cfg.API.OIDCClientClaim`. Reword the `isIDToken`
  comment to "a `typ` claim of `ID`, which some providers put in the
  payload" (behaviour unchanged).
- [x] 1.3 **Fixtures.** `internal/api/oidctest`: `Token.Claims
  map[string]any`, merged last over the defaults so a test can add
  `cid`, `scp`, `roles`, `appid`, a namespaced key, or override `aud`
  with a string (OQ1). Add three documented helpers that return a
  `*Token`: `Okta(client, groups)` (`cid`, `scp: ["openid"]`, `uid`,
  no `azp`, no `preferred_username`), `Entra(appid, roles)` (`appid`,
  `roles`, `groups` as GUIDs, `aud` string), `Namespaced(groups)`
  (`https://example.com/groups`).
- [x] 1.4 **Tests.** In `internal/api/api_test.go` (where the authn
  cases live), a table over `{ClientClaim, GroupsClaim, NameClaim,
  token}` asserting `Claims.Client`, `Claims.Groups`, `Claims.Name`:
  Keycloak-shaped with `azp`; Okta-shaped with `ClientClaim: "cid"`;
  Entra-shaped with `ClientClaim: "appid"`, `GroupsClaim: "roles"`;
  namespaced groups; and the property the authz warning relies on:
  `ClientClaim: "cid"` on a Keycloak-shaped token yields an empty
  `Client`, so `AuthzConfig.Scope` grants nothing from `clients`.
  Prove non-vacuity once: point the Okta case at `ClientClaim: "azp"`
  and watch it fail ([[feedback-verify-test-non-vacuous]]).
  _Done 2026-10-25: `TestAuthn_ProviderClaimShapes` (five shapes) and
  `TestAuthz_UnconfiguredClientClaimGrantsNothing`; with the Okta case
  on `azp` it failed (`Client:""`), reverted._
- [x] 1.5 **Call sites.** The three hard-coded `NameClaim`/`GroupsClaim`
  literals (`api_test.go`, `status_http_test.go`,
  `apiendpoints_integration_test.go`) gain `ClientClaim: "azp"` through
  one shared helper, so the next claim added touches one place.
- [x] 1.6 **Metrics and docs in code.** No new metric: a token whose
  client claim is absent is not a refusal. Update the `AuthzConfig`
  doc comment ("by the claim `OIDC_CLIENT_CLAIM` names") and the
  `clients` warning in `authz.go`.
  _Done: `authz.go` has no runtime warning to reword, so the `Clients`
  field comment now carries it (an unmatched key yields DefaultOrgs);
  `Claims.Client` in `principal.go` likewise._

<!--docz:tasks:end-->

<!--docz:criteria:start-->
#### Success Criteria

- `make test` passes; the new table has at least five cases and the
  non-vacuity check was performed and reverted (noted on the task).
- `OIDC_CLIENT_CLAIM` unset behaves exactly as before: the full
  `internal/api` and `internal/store/postgres` integration suites pass
  without any test setting it.
- `make lint` passes with no new `nolint`.

<!--docz:criteria:end-->
<!--docz:phase:end-->

---

<!--docz:phase:start-->
### Phase 2: The BFF's token endpoint method and authorization parameters

DESIGN-0038 B3.3, B3.4, B3.5; D6, D7; OQ5, OQ6.

<!--docz:tasks:start-->
#### Tasks

- [x] 2.1 **Config.** `ui/server/config.ts`: `oidc.tokenAuthMethod:
  "client_secret_post" | "client_secret_basic"` from
  `OIDC_TOKEN_AUTH_METHOD` (default post; any other value is a
  `ConfigError` naming the two accepted values) and
  `oidc.authorizationParams: Record<string, string>` from
  `OIDC_AUTHORIZATION_PARAMS` (JSON object of string → string; default
  `{}`; parse errors, non-string values and the reserved keys
  `redirect_uri`, `scope`, `state`, `nonce`, `code_challenge`,
  `code_challenge_method`, `response_type`, `client_id` are
  `ConfigError`s listing every offending key). Tests in
  `ui/server/config.test.ts` for each refusal and the defaults.
- [x] 2.2 **Discovery.** `ui/server/oidc.ts`: pick
  `client.ClientSecretPost` or `client.ClientSecretBasic` from the
  config. Test with the mock issuer in both modes (2.4).
- [x] 2.3 **Authorize URL.** `ui/server/auth.ts`: spread
  `config.oidc.authorizationParams` **before** the fixed parameters in
  `buildAuthorizationUrl`, so the fixed ones win even if validation is
  bypassed; test that `audience=api://x` appears and that a reserved
  key cannot override `state`.
- [x] 2.4 **Mock issuer.** `ui/server/testing/mock-issuer.ts` accepts
  the client secret from the POST body or an `Authorization: Basic`
  header (RFC 6749 §2.3.1 form-encoding of id and secret), records
  which was used on the issuer object for assertions, and echoes any
  `audience` query parameter from `/authorize` into the access token's
  `aud` array when present (so the authorization-params path is
  observable end to end). OQ2.
- [x] 2.5 **e2e.** `ui/e2e/stack.ts`: the `no-remediation` BFF starts
  with `OIDC_TOKEN_AUTH_METHOD=client_secret_basic`; its sign-in spec
  already proves the flow, so no new spec, just one assertion in
  `no-remediation.spec.ts` that sign-in succeeded (it does today).
- [x] 2.6 **README.** `ui/README.md` § Configuration gains the two
  variables with defaults and the reserved-key rule.

<!--docz:tasks:end-->

<!--docz:criteria:start-->
#### Success Criteria

- `make lint-ui` and `make test-ui` pass; `bun run test` covers both
  token methods against the mock issuer and every reserved key.
- `make test-ui-e2e` passes with the `chromium` project on post and the
  `no-remediation` project on basic.
- An unset environment produces byte-identical authorize URLs and token
  requests to today's (assert in a test by comparing against a recorded
  fixture from before the change).

<!--docz:criteria:end-->
<!--docz:phase:end-->

---

<!--docz:phase:start-->
### Phase 3: Chart values and schema

DESIGN-0038 B4; OQ7.

<!--docz:tasks:start-->
#### Tasks

- [x] 3.1 **Values.** `charts/repo-guardian/values.yaml`:
  `api.auth.clientClaim: azp`; `ui.oidc.tokenAuthMethod:
  client_secret_post`; `ui.oidc.authorizationParams: {}`;
  `ui.oidc.scopes: [openid, profile, email, offline_access]` with a
  comment that Keycloak's audience and group mappers need no `groups`
  scope and that Okta's custom servers reject an undefined one;
  `api.authz.clients` comment reworded to "by the claim
  `api.auth.clientClaim` names".
- [x] 3.2 **Schema.** `values.schema.json`: `api.auth.nameClaim`,
  `groupsClaim`, `clientClaim` (`string`, `minLength: 1`);
  `ui.oidc.tokenAuthMethod` (`enum`); `ui.oidc.authorizationParams`
  (`object`, `additionalProperties: {type: string}`, and a `not` over
  `required: [<reserved key>]` for each of the eight reserved keys so
  the schema refuses them too, OQ3).
- [x] 3.3 **Render.** `_helpers.tpl` api env gains `OIDC_CLIENT_CLAIM`;
  `templates/ui.yaml` gains `OIDC_TOKEN_AUTH_METHOD` and, only when
  the map is non-empty, `OIDC_AUTHORIZATION_PARAMS` as `toJson`.
  `validateUI` fails the render naming the reserved key when
  `authorizationParams` contains one (belt and braces with 3.2, and
  the message is the one operators see).
  _Note 2026-10-25: helm validates the schema before rendering, so with
  3.2 in place an operator sees the schema's `'allOf' failed` at
  `/ui/oidc/authorizationParams`; the named `validateUI` message is
  reached only when schema validation is skipped (verified by removing
  the schema rule once)._
- [x] 3.4 **Tests.** `tests/ui_test.yaml`: update the `OIDC_SCOPES`
  default assertion; add `OIDC_TOKEN_AUTH_METHOD` default, the JSON
  rendering of a two-key `authorizationParams`, absence of the env when
  empty, and a `failedTemplate` case for `authorizationParams.state`.
  `tests/api_*` (wherever the api env is asserted): `OIDC_CLIENT_CLAIM`.
  `tests/values_guard_test.yaml` or a schema test: an unknown
  `tokenAuthMethod` fails.
  _Done: the two refusal cases live in `tests/ui_guards_test.yaml`,
  which renders `deployment.yaml` where `validateUI` runs;
  `OIDC_CLIENT_CLAIM` is asserted in `tests/topology_test.yaml`._
- [x] 3.5 **README.** `make helm-docs` regenerates the chart README from
  `README.md.gotmpl`; add a sentence under "The UI" pointing at the
  identity-provider page (Phase 4) in the `.gotmpl`, never the rendered
  README.

<!--docz:tasks:end-->

<!--docz:criteria:start-->
#### Success Criteria

- `helm unittest charts/repo-guardian` passes with the new cases; the
  reserved-key render failure is proven non-vacuous once (set a
  reserved key, see the failure, revert).
- `helm lint` and the schema accept the default values and the
  `examples/values-*.yaml` files unchanged.
- The rendered UI Deployment for default values differs from today's
  only in `OIDC_SCOPES` and the added `OIDC_TOKEN_AUTH_METHOD`.

<!--docz:criteria:end-->
<!--docz:phase:end-->

---

<!--docz:phase:start-->
### Phase 4: The identity-provider page and the recipes

DESIGN-0038 B1, B5, B6; D4; OQ8.

<!--docz:tasks:start-->
#### Tasks

- [x] 4.1 **The page.** `docs/operations/identity-provider.md`: the
  contract table from DESIGN-0038 B1 (requirement, API, UI, why) with
  the three new settings; the token-flow mermaid diagram from B2; a
  "What the chart exposes" table mapping every `api.auth.*` and
  `ui.oidc.*` value to its env var.
- [x] 4.2 **Keycloak.** Move `docs/usage/api.md` § Keycloak example into
  the page unchanged in substance, under the five recipe headings
  (issuer and audience, UI client, groups, machine clients, chart
  values); `api.md` keeps § Authentication's rules (reworded to drop
  "Keycloak's `typ: ID`") and links the page.
- [x] 4.3 **Okta.** Custom authorization server required (org server
  tokens cannot be validated by third parties); issuer
  `https://<org>.okta.com/oauth2/<id>`; audience; `groups` claim on
  access tokens with a group filter; web app with refresh tokens, the
  redirect and sign-out URIs, `client_secret_post` or
  `tokenAuthMethod: client_secret_basic`; scopes default (no `groups`
  unless defined); `nameClaim: sub` or a custom claim; machine clients
  via `clientClaim: cid` or a groups claim on client-credentials
  tokens.
- [x] 4.4 **Entra ID.** Two app registrations; `accessTokenAcceptedVersion:
  2`; issuer `https://login.microsoftonline.com/<tenant>/v2.0`; audience
  the API app's client ID or `api://<id>`; the API scope in
  `ui.oidc.scopes`; GUID groups or `groupsClaim: roles`; the 200-group
  overage caveat (falls to `defaultOrgs`); `clientClaim: azp` on v2
  tokens; the session-cookie size note (`session.ts` ten chunks).
- [x] 4.5 **Auth0.** Trailing-slash issuer; API identifier as audience;
  `ui.oidc.authorizationParams: {audience: <identifier>}`; a post-login
  Action adding a namespaced groups claim set as `groupsClaim`;
  RP-initiated logout enabled for `end_session_endpoint`;
  `offline_access` on the API; `clientClaim: azp`.
- [x] 4.6 **Google and others.** One paragraph: opaque access tokens, use
  a broker (Keycloak, Dex, Okta, Auth0) and follow its recipe. A
  closing "Any other provider" checklist derived from the contract
  table.
- [x] 4.7 **Verifying.** § Verifying a provider: sign in, open `/me`
  through the UI, confirm `name`, `groups` and the visible orgs; the
  `repo_guardian_api_auth_failures_total{reason}` table mapping each
  reason (`issuer`, `audience`, `algorithm`, `id_token`, `signature`,
  `expired`, `not_yet_valid`, `malformed`) to the recipe step that
  fixes it. IMPL-0029 8.9 and IMPL-0032 9.3 point here.
- [x] 4.8 **Links.** `docs/usage/ui.md` (replace the Keycloak link and
  the example issuer with a neutral one), `docs/operations/v2-onboarding.md`
  § Prerequisites (one bullet), `charts/repo-guardian/README.md.gotmpl`
  (3.5), `mkdocs.yml` Operations nav entry, CLAUDE.md § IMPL-0029 Phase 8
  bullet gains the three env vars in one line.
  _Done 2026-10-25. Criteria check: all six `api.auth.*` and four
  `ui.oidc.*` keys from `values.yaml` appear in the page's table
  (grepped); four recipes × five headings = 20; `api.md` names no
  provider; markdownlint clean._

<!--docz:tasks:end-->

<!--docz:criteria:start-->
#### Success Criteria

- Every `api.auth.*` and `ui.oidc.*` value appears in the page's table
  (grep the values file against the page in the task note).
- `docs/usage/api.md` contains no provider name other than in the link
  to the page; `markdownlint-cli2` passes on the new page.
- Each recipe has the same five headings in the same order.

<!--docz:criteria:end-->
<!--docz:phase:end-->

---

<!--docz:phase:start-->
### Phase 5: rc.17

The release-bump pull request, by the procedure CLAUDE.md § Release
and the v2-branch bullet describe (rc.16 was PR #239).

<!--docz:tasks:start-->
#### Tasks

- [ ] 5.1 **Bump.** `charts/repo-guardian/Chart.yaml` `version` and
  `appVersion` to `2.0.0-rc.17`; `ui/CHANGELOG.yaml` entry (title
  "Identity providers beyond Keycloak", tags `new`, `api`, `ui`,
  `chart`; the summary must not contain a string any Playwright
  locator matches by substring, see the rc.16 lesson in #239);
  `cd ui && bun run gen:changelog`; `make helm-docs`; `make lint-ui`
  green locally.
- [ ] 5.2 **PR.** `dont-release` against `v2`; CI green (the `ui` and
  `helm` filters run on `Chart.yaml`).
- [ ] 5.3 **Tag and publish.** After merge: signed annotated tag
  `v2.0.0-rc.17` on the merge commit, `gh workflow run ghcr.yml --ref
  v2.0.0-rc.17 -f tag=v2.0.0-rc.17`; all twelve jobs green; `cosign
  verify` on the image; `helm pull --version 2.0.0-rc.17` reports the
  appVersion; `latest` unchanged.
- [ ] 5.4 **Homelab** (`deferred - human required`): upgrade in place,
  confirm Keycloak sign-in and `/me` unchanged with no values change;
  tick IMPL-0029 8.9 / IMPL-0032 9.3 using the Phase 4.7 procedure.

<!--docz:tasks:end-->

<!--docz:criteria:start-->
#### Success Criteria

- rc.17 image, UI image and chart are published and signed with SLSA
  provenance; the status page's Changelog card shows rc.17 as running.
- The homelab needed no IdP or values change to upgrade.

<!--docz:criteria:end-->
<!--docz:phase:end-->

---

<!--docz:file-changes:start-->
## File Changes

| File | Action | Description |
| ---- | ------ | ----------- |
| `internal/config/api.go`, `role_test.go` | Modify | `OIDC_CLIENT_CLAIM` |
| `internal/api/authn.go`, `authz.go` | Modify | `ClientClaim`; drop `AZP`; comments |
| `internal/api/oidctest/oidctest.go` | Modify | `Token.Claims`, `Okta`/`Entra`/`Namespaced` helpers |
| `internal/api/api_test.go`, `status_http_test.go`, `internal/store/postgres/apiendpoints_integration_test.go` | Modify | shared authn config helper; the claim table |
| `cmd/repo-guardian/api.go` | Modify | pass `ClientClaim` |
| `ui/server/config.ts`, `config.test.ts` | Modify | `tokenAuthMethod`, `authorizationParams`, validation |
| `ui/server/oidc.ts`, `auth.ts`, tests | Modify | method selection; extra params before fixed ones |
| `ui/server/testing/mock-issuer.ts` | Modify | basic auth; `audience` echo |
| `ui/e2e/stack.ts`, `e2e/no-remediation.spec.ts` | Modify | basic method on the second BFF |
| `ui/README.md` | Modify | env table |
| `charts/repo-guardian/values.yaml`, `values.schema.json`, `templates/_helpers.tpl`, `templates/ui.yaml` | Modify | new values, schema, env, guard |
| `charts/repo-guardian/tests/ui_test.yaml` and the api env test | Modify | new assertions; scopes default |
| `charts/repo-guardian/README.md.gotmpl` (+ regenerated README) | Modify | pointer to the page |
| `docs/operations/identity-provider.md` | Create | contract, recipes, verification |
| `docs/usage/api.md`, `docs/usage/ui.md`, `docs/operations/v2-onboarding.md`, `mkdocs.yml`, `CLAUDE.md` | Modify | links and rewording |
| `charts/repo-guardian/Chart.yaml`, `ui/CHANGELOG.yaml`, `ui/web/src/generated/changelog.json` | Modify | rc.17 |

<!--docz:file-changes:end-->

<!--docz:testing:start-->
## Testing Plan

- [ ] Go: the claim table in `internal/api` (five shapes, one
  non-vacuity check), config validation cases, existing suites
  unchanged with the variable unset.
- [ ] BFF: config refusals per reserved key and per bad method; authorize
  URL with and without extra params; token request in both methods
  against the mock issuer; recorded-fixture equality for the unset
  environment.
- [ ] e2e: both Playwright projects, one per method.
- [ ] Chart: helm-unittest for each env var, the default change, the
  reserved-key failure (proven once), schema rejection of a bad enum.
- [ ] Docs: `markdownlint-cli2`; values-to-page coverage grep.

<!--docz:testing:end-->

<!--docz:dependencies:start-->
## Dependencies

- DESIGN-0038 merged (PR #240).
- `openid-client` v6 (already in `ui/package.json`) exposes
  `ClientSecretBasic` and `ClientSecretPost`.
- No schema, migration or OpenAPI change; no new Go dependency.

<!--docz:dependencies:end-->

## Open Questions

**OQ1. How `oidctest` grows provider shapes.**

- (a) ✅ recommended: **a `Token.Claims map[string]any` merged last**
  plus three named constructors (`Okta`, `Entra`, `Namespaced`); one
  mechanism covers the next provider without a new field, and the
  constructors keep the tests readable.
- (b) one boolean per shape (`Okta bool`, `Entra bool`); mirrors
  `IDToken`/`Rogue`, but every new claim variation is a new field.
- other:

**Decision (2026-10-25):** (a).

**OQ2. How the mock issuer exercises `client_secret_basic`.**

- (a) ✅ recommended: **accept both methods always and record which
  one was used**; the e2e `no-remediation` BFF runs on basic, the
  `chromium` one on post, and a unit test asserts the recorded method.
  No flag to thread through the stack.
- (b) a `tokenAuth: "post" | "basic"` option on the issuer that
  refuses the other; stricter, but the stack then needs two issuers or
  a third BFF to cover both.
- other:

**Decision (2026-10-25):** (a).

**OQ3. Where reserved authorization parameters are refused.**

- (a) ✅ recommended: **in all three places**: BFF config validation
  (the runtime truth), `validateUI` (the message operators see at
  render) and the schema (`not`/`required` per key, so `helm lint`
  catches it too).
- (b) BFF and `validateUI` only; the schema stays simpler and the
  render still fails.
- other:

**Decision (2026-10-25):** (a).

**OQ4. The `OIDC_AUTHORIZATION_PARAMS` wire format.**

- (a) ✅ recommended: **a JSON object**, rendered by the chart with
  `toJson`; unambiguous for values containing `=` or `,` (Entra's
  `domain_hint` values, Auth0 audiences with `/`).
- (b) `key=value,key=value`; shorter to type by hand, ambiguous for
  the values above.
- other:

**Decision (2026-10-25):** (a).

**OQ5. What else rides in rc.17.**

- (a) ✅ recommended: **only this IMPL**; the rc exists to put the
  identity knobs in operators' hands before the GA docs name them.
- (b) also the `internal/controls/file/file.go` quadratic
  duplicate-key `FIX:`; a real cost but unrelated, and IMPL-0034 Phase 9
  carries it.
- other:

**Decision (2026-10-25):** (a).

<!--docz:references:start-->
## References

- [DESIGN-0038](../design/0038-v200-ga-cutover-and-identity-provider-portability.md) Part B.
- [DESIGN-0027](../design/0027-v2-read-only-api-business-ui-and-status-page.md) § Authentication, § Authorization, OQ22.
- `internal/api/authn.go`, `authz.go`, `oidctest/oidctest.go`;
  `ui/server/{config,oidc,auth}.ts`, `ui/server/testing/mock-issuer.ts`,
  `ui/e2e/stack.ts`; `charts/repo-guardian/{values.yaml,values.schema.json,templates/_helpers.tpl,templates/ui.yaml}`.
- `docs/usage/api.md` § Keycloak example (moves), `docs/usage/ui.md`.
- IMPL-0029 8.9, IMPL-0032 9.3 (the group-split check this page's
  procedure satisfies); PR #239 (the rc.16 bump, the substring-locator
  lesson).

<!--docz:references:end-->
