# The read-only API

repo-guardian v2 serves a read-only JSON API over the findings store
(DESIGN-0027). It backs the business UI and the public status page,
and anything else that wants compliance data: a dashboard, a CI gate,
a report. Every endpoint is a `GET`.

The contract is [`api/openapi.yaml`](https://github.com/donaldgifford/repo-guardian/blob/main/api/openapi.yaml).
The running API serves it at `/api/v1/openapi.yaml` without a token, and
every release attaches it as `openapi.yaml`.

<!--toc:start-->
- [Enabling it](#enabling-it)
- [Authentication](#authentication)
  - [Keycloak example](#keycloak-example)
- [Authorization](#authorization)
- [Endpoints](#endpoints)
- [The status page](#the-status-page)
- [Pagination](#pagination)
- [Errors](#errors)
- [Compatibility](#compatibility)
<!--toc:end-->

## Enabling it

```yaml
api:
  enabled: true
  auth:
    issuer: https://keycloak.example.com/realms/platform
    audience: repo-guardian-api
  authz:
    groups:
      platform-engineering: ["*"]
      team-web: ["acme"]
```

In topology `split` this renders an `api` Deployment (two replicas and a
PDB) and a ClusterIP Service, `<release>-repo-guardian-api`. In `all` the
API runs inside the one Deployment on `api.port` and the same Service
targets it. The Service is never published by the chart: people reach
the API through the UI, and machines from inside the cluster or through
an ingress you own.

The API reads Postgres through a read-only role. See the chart README,
"The API's read-only role", for how each Postgres mode provides it.

## Authentication

Every endpoint except `/status` and `/openapi.yaml` needs
`Authorization: Bearer <access token>`, an OIDC **access token** issued
for the API's audience. The API:

- discovers the issuer (`api.auth.issuer`) and verifies the signature
  against its JWKS (RS256 or ES256 only);
- requires `iss` to equal the issuer exactly and `aud` to contain
  `api.auth.audience`;
- allows 60 seconds of clock skew on `exp` and `nbf`;
- **refuses ID tokens**: anything carrying a `nonce` or `at_hash`, or
  Keycloak's `typ: ID`;
- reads the caller's name from `api.auth.nameClaim`
  (default `preferred_username`) and groups from `api.auth.groupsClaim`
  (default `groups`).

Until discovery succeeds the API answers `503` and is not ready.
Refusals are counted in `repo_guardian_api_auth_failures_total{reason}`.

`api.auth.enabled: false` turns authentication off for a pod-network-only
API. The chart then refuses to render the UI or its Ingress (INV-0009).

### Keycloak example

In the realm the UI and API share:

1. **API client.** Create a client `repo-guardian-api` with every flow
   off. It exists only to be an audience.
2. **UI client.** Create a confidential client `repo-guardian-ui` with
   the standard flow on, PKCE `S256`, the redirect URI
   `https://guardian.example.com/auth/callback`, and the post-logout
   redirect URI `https://guardian.example.com/`.
3. **Audience.** Give the UI client a dedicated scope mapper of type
   *Audience*, with included client audience `repo-guardian-api` and
   "Add to access token" on. Tokens the UI obtains are then valid for
   the API.
4. **Groups.** Add a *Group Membership* mapper with token claim name
   `groups`, "Full group path" off and "Add to access token" on.
5. **Machine clients** (optional). Create a confidential client with
   service accounts on and the same audience mapper, then map its
   `azp` under `api.authz.clients` (below).

The issuer is `https://keycloak.example.com/realms/<realm>`.

## Authorization

`api.authz` maps groups to the orgs they may see. It is rendered into a
ConfigMap, read at startup, and a checksum annotation rolls the API
pods when it changes.

```yaml
api:
  authz:
    groups:
      platform-engineering: ["*"]      # every org
      team-web: ["acme", "acme-labs"]
    defaultOrgs: []                     # for callers matching no group
    clients:
      ci-dashboard: [platform-engineering]
```

- A caller sees the union of its groups' orgs. `"*"` is every org.
- `defaultOrgs` applies only when none of the caller's groups is mapped.
  Leave it empty so an unmapped user sees nothing.
- `clients` maps a machine client's `azp` to groups defined above. It
  is for IdPs that put no groups on client-credentials tokens. **Never
  map the UI's client ID here.** Every user token from the UI carries
  `azp: repo-guardian-ui`, so mapping it would grant every signed-in
  user those groups.
- Scoping is applied in SQL on every query, including every page of a
  list. An org the caller cannot see answers `404`, the same as one that
  does not exist. A caller who can see no org gets `403` everywhere
  except `/me`, and the UI turns that into an "ask for access" page.

## Endpoints

All live under `/api/v1`. The OpenAPI document has the full schemas.

| Endpoint | Returns |
| --- | --- |
| `/status` | public service status (below) |
| `/me` | the caller, and the orgs it may see |
| `/summary` | tracked and parked repositories, compliance, findings by status, open PRs by age, stale PRs |
| `/rules`, `/rules/{kind}/{name}` | compliance per rule with trend; one rule by org, with top reasons and oldest failures |
| `/orgs`, `/orgs/{org}` | compliance per org; one org by rule, and why rules did not apply |
| `/findings` | findings filtered by `status`, `reason`, `remediation`, `org`, `kind`, `rule`, `pr_stale`, `since_before` |
| `/repositories`, `/repositories/{id}` | repositories filtered by `org`, `active`, `park_reason`, `q`; one repository with its findings |
| `/repositories/{id}/events`, `/repositories/{id}/checks` | a repository's timeline and recent checks |
| `/compliance/history` | snapshot series by `org`, `kind`, `rule`, `from`, `to` |
| `/policy` | the current policy version, its rollout and its rules |
| `/installations` | installations and their last rate-limit snapshot |
| `/openapi.yaml` | the contract (public) |

**Compliance** everywhere is `compliant / (compliant + non_compliant)`
over active repositories, floored to one decimal. `not_applicable` and
`unknown` are counted separately. When nothing is measured the value is
`null`, never 100. `repo-guardian report` computes the same number.

**Evidence** explains a finding and is keyed by `reason`. Values in it
came from repositories (paths, assertion messages, PR titles): treat
them as data and never render them as markup. Build PR links from
`org`, `repository` and `pr.number` rather than trusting `pr.url`.

`pr_stale` compares an open repo-guardian PR's age with `stale_after`,
which defaults to `api.prStaleAfter` (720h) and can be set per request
with `?stale_after=`.

```bash
curl -s -H "Authorization: Bearer $TOKEN" \
  "https://guardian.example.com/api/v1/findings?status=non_compliant&org=acme&limit=200"
```

## The status page

`GET /api/v1/status` is public when `api.status.public` is true (the
default). It reports an overall state, one state per component
(checks, webhooks, discovery, snapshots, GitHub budget) with a
fixed-template detail such as "last success 3m ago", and fleet-wide
compliance. It names no org, repository, rule or installation and
carries no error text. The API computes it every 30 seconds in memory,
so a request never reaches the database.

## Pagination

Lists return `items` and, when there is more, `next_cursor`. Pass it
back unchanged as `?cursor=`, with the same filters, to get the next
page. `limit` defaults to 50 and is capped at 200.

Cursors are keyset positions, not offsets, so pages stay stable while
checks write. A cursor is bound to the filters it was issued with:
sending it with different filters is a `400`. Cursors are opaque and
unsigned. Tampering with one cannot widen what you see, because scoping
is applied to every page.

## Errors

Errors are RFC 9457 `application/problem+json`:

```json
{"type": "about:blank", "title": "Not Found", "status": 404, "detail": "no such org", "request_id": "c0ffee…"}
```

| Status | When |
| --- | --- |
| `400` | a malformed filter, `limit` or cursor, or a cursor from other filters |
| `401` | no token, or a token that fails verification; carries `WWW-Authenticate: Bearer` |
| `403` | the caller can see no org |
| `404` | no such resource, or one in an org the caller cannot see |
| `503` | the issuer has not been discovered yet |

`request_id` matches the API's logs. Quote it when reporting a problem.

## Compatibility

`/api/v1` changes only additively:

- new endpoints, new optional query parameters and new response fields
  may appear;
- enums documented as *open* (status, reason, remediation, park reason,
  trigger, component) may gain values, so treat an unknown value as
  opaque rather than failing;
- nothing is removed or renamed, and no field changes type.

Anything else would be `/api/v2`, served beside v1 for a deprecation
window. The UI and the API are released together from one tag, so the
UI always matches its API.
