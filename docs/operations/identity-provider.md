# Identity providers

<!-- markdownlint-disable-file MD024 -->

The read-only API and the business UI sign people in with OpenID
Connect against one issuer, `api.auth.issuer`. Nothing in either is
tied to one product: this page states what they need from a provider,
maps every chart value to what it configures, and gives a recipe for
Keycloak, Okta, Microsoft Entra ID and Auth0. Read the contract first;
every recipe is the contract applied to one provider.

<!--toc:start-->
- [The contract](#the-contract)
- [The token flow](#the-token-flow)
- [What the chart exposes](#what-the-chart-exposes)
- [Keycloak](#keycloak)
- [Okta](#okta)
- [Microsoft Entra ID](#microsoft-entra-id)
- [Auth0](#auth0)
- [Google and other providers](#google-and-other-providers)
- [Verifying a provider](#verifying-a-provider)
<!--toc:end-->

## The contract

| Requirement | API | UI (BFF) | Why |
| --- | --- | --- | --- |
| OIDC discovery at the issuer URL | reads `jwks_uri` | reads `authorization_endpoint`, `token_endpoint`, optional `end_session_endpoint`, `revocation_endpoint` | nothing is hand-configured |
| Access tokens are JWTs signed RS256 or ES256 | verifies | forwards | the API never calls an introspection endpoint |
| `iss` equals the configured issuer exactly | checks | — | a trailing slash or a v1/v2 endpoint variant is a different issuer |
| `aud` contains the API audience | checks | — | the provider must put the API's audience on the UI client's tokens |
| A groups claim as a **top-level** key, array or string | reads `OIDC_GROUPS_CLAIM` | — | nested claim paths are not supported |
| A name claim (any string claim) | reads `OIDC_NAME_CLAIM` | display name from the ID token: `name`, `preferred_username`, `email`, `sub` | logs and `/me` |
| A client claim on machine tokens | reads `OIDC_CLIENT_CLAIM` | — | `api.authz.clients`; the claim name differs per provider |
| Authorization code + PKCE S256 for a confidential client | — | always | the BFF holds the secret; the browser holds only the session cookie |
| Token endpoint authentication method | — | `OIDC_TOKEN_AUTH_METHOD`: `client_secret_post` or `client_secret_basic` | providers default differently |
| Refresh tokens on the UI client | — | `offline_access` or the provider's equivalent | the session is 8h; access tokens are shorter |
| Redirect URI `<public URL>/auth/callback`; post-logout `<public URL>/` | — | fixed | both must be registered on the client |
| Optional `audience`/`resource` at authorize time | — | `OIDC_AUTHORIZATION_PARAMS` | Auth0 returns opaque tokens without it |
| No ID tokens at the API | refuses `nonce`, `at_hash`, `typ: ID` | sends the access token only | DESIGN-0027 |

## The token flow

```mermaid
sequenceDiagram
  autonumber
  participant Br as Browser
  participant BFF as UI BFF (Bun/Hono)
  participant IdP as Identity provider
  participant API as repo-guardian api
  Br->>BFF: GET /auth/login
  BFF-->>Br: 302 authorize (PKCE S256, state, nonce, scopes, [audience])
  Br->>IdP: authenticate
  IdP-->>Br: 302 /auth/callback?code
  Br->>BFF: GET /auth/callback
  BFF->>IdP: POST token (code, verifier; client_secret_post | client_secret_basic)
  IdP-->>BFF: access token (JWT, aud = API), refresh token, ID token
  BFF-->>Br: Set-Cookie __Host-rg_session (sealed)
  Br->>BFF: GET /api/v1/…
  BFF->>API: Authorization: Bearer <access token>
  API->>IdP: JWKS (cached)
  API-->>API: iss, aud, alg, exp/nbf, not an ID token; name, groups, client claims
  API-->>BFF: scoped response (groups → orgs)
  BFF-->>Br: response
```

Step 2 carries `ui.oidc.authorizationParams`, step 6 uses
`ui.oidc.tokenAuthMethod`, and step 13 reads the three claims the
`api.auth.*Claim` values name.

## What the chart exposes

| Value | Environment variable | Role | Default | What it sets |
| --- | --- | --- | --- | --- |
| `api.auth.enabled` | `API_AUTH_ENABLED` | api | `true` | require a bearer token on every data endpoint |
| `api.auth.issuer` | `OIDC_ISSUER` | api, ui | required | the issuer both trust; discovery runs against it |
| `api.auth.audience` | `OIDC_AUDIENCE` | api | `repo-guardian-api` | the `aud` value an access token must contain |
| `api.auth.nameClaim` | `OIDC_NAME_CLAIM` | api | `preferred_username` | the caller's name in logs and `/me` |
| `api.auth.groupsClaim` | `OIDC_GROUPS_CLAIM` | api | `groups` | the claim matched against `api.authz.groups` |
| `api.auth.clientClaim` | `OIDC_CLIENT_CLAIM` | api | `azp` | the claim matched against `api.authz.clients` |
| `ui.oidc.clientId` | `OIDC_CLIENT_ID` | ui | `repo-guardian-ui` | the UI's confidential client |
| `ui.existingSecret` key `oidc-client-secret` | `OIDC_CLIENT_SECRET` | ui | required | that client's secret |
| `ui.oidc.scopes` | `OIDC_SCOPES` | ui | `[openid, profile, email, offline_access]` | the scopes requested at login; must include `openid` |
| `ui.oidc.tokenAuthMethod` | `OIDC_TOKEN_AUTH_METHOD` | ui | `client_secret_post` | how the secret reaches the token and revocation endpoints |
| `ui.oidc.authorizationParams` | `OIDC_AUTHORIZATION_PARAMS` | ui | `{}` | extra authorize parameters, rendered as JSON only when set |

`ui.oidc.authorizationParams` may not set `redirect_uri`, `scope`,
`state`, `nonce`, `code_challenge`, `code_challenge_method`,
`response_type` or `client_id`: the login flow sets them. The chart's
schema refuses them, and so does the UI at startup.

**Never map the UI's client ID under `api.authz.clients`.** Every user
token the UI obtains carries it in the client claim, so mapping it
would grant every signed-in user those groups.

## Keycloak

Keycloak works on the chart defaults.

### Issuer and audience

The issuer is `https://<host>/realms/<realm>`. Create a client
`repo-guardian-api` with every flow off; it exists only to be the
audience.

### UI client

Create a confidential client `repo-guardian-ui` with the standard flow
on, PKCE `S256`, the redirect URI `https://guardian.example.com/auth/callback`
and the post-logout redirect URI `https://guardian.example.com/`. Give
it a dedicated scope mapper of type *Audience*, included client
audience `repo-guardian-api`, "Add to access token" on, so the tokens
the UI obtains are valid for the API.

### Groups

Add a *Group Membership* mapper with token claim name `groups`, "Full
group path" off and "Add to access token" on. Group names are the
`api.authz.groups` keys.

### Machine clients

Create a confidential client with service accounts on and the same
audience mapper, then map its client ID under `api.authz.clients`.
Keycloak puts it in `azp`, the default client claim.

### Chart values

```yaml
api:
  auth:
    issuer: https://keycloak.example.com/realms/platform
    audience: repo-guardian-api
```

## Okta

### Issuer and audience

Use a **custom authorization server** (API Access Management). Tokens
from the org authorization server cannot be validated by anyone but
Okta. The issuer is `https://<org>.okta.com/oauth2/<server id>`
(`default` for the default custom server), and the audience is the
server's audience: `api://default` or one you choose.

### UI client

Create a web app integration with the authorization code grant and
refresh tokens on. Register the sign-in redirect URI
`https://guardian.example.com/auth/callback` and the sign-out redirect
URI `https://guardian.example.com/`. Okta accepts
`client_secret_post`; if the app is set to `client_secret_basic`, set
`ui.oidc.tokenAuthMethod` to match. Keep the chart's default scopes:
a custom server rejects a scope it does not define, so do not add
`groups` unless the server defines it.

### Groups

On the authorization server, add a `groups` claim to **access**
tokens, value type *Groups*, with a filter selecting the groups
repo-guardian should see. Group names are the `api.authz.groups` keys.
Okta access tokens carry `sub` and `uid` but not `preferred_username`,
so set `api.auth.nameClaim: sub` or add a custom claim and name it.

### Machine clients

A client-credentials token names its client in `cid`. Set
`api.auth.clientClaim: cid` and map the client ID under
`api.authz.clients`, or add a `groups` claim for client-credentials
tokens and map groups instead.

### Chart values

```yaml
api:
  auth:
    issuer: https://example.okta.com/oauth2/default
    audience: api://default
    nameClaim: sub
    clientClaim: cid
ui:
  oidc:
    tokenAuthMethod: client_secret_basic   # only if the app requires it
```

## Microsoft Entra ID

### Issuer and audience

Register two applications. In the **API** registration, expose a scope
(for example `access`) and set `accessTokenAcceptedVersion` to `2` in
the manifest, so tokens carry the v2 issuer the discovery document
names. The issuer is `https://login.microsoftonline.com/<tenant>/v2.0`.
The audience is the API application's client ID (or its
`api://<client id>` application ID URI, whichever the tokens carry).

### UI client

In the **UI** registration, add the web platform with the redirect URIs
`https://guardian.example.com/auth/callback` and
`https://guardian.example.com/` (Entra ID checks the post-logout
redirect against the same list), create a client secret, and
grant it the API's scope. Put that scope in `ui.oidc.scopes`; without
it the access token is for Microsoft Graph, not the API.

### Groups

Groups arrive as object-ID GUIDs, so the `api.authz.groups` keys are
GUIDs. App roles read better: assign roles on the API registration and
set `api.auth.groupsClaim: roles`. A user in more than 200 groups gets
an overage claim and no `groups` at all, which falls to `defaultOrgs`;
prefer app roles for that reason. Many groups also make all three
tokens large: the session cookie splits into at most ten chunks, and a
sign-in whose tokens exceed that fails rather than being raised.

### Machine clients

v2 tokens name the client in `azp`, the default client claim. v1 tokens
use `appid`; set `api.auth.clientClaim: appid` only if you keep the API
on v1 tokens.

### Chart values

```yaml
api:
  auth:
    issuer: https://login.microsoftonline.com/00000000-0000-0000-0000-000000000000/v2.0
    audience: 11111111-1111-1111-1111-111111111111
    groupsClaim: roles
ui:
  oidc:
    clientId: 22222222-2222-2222-2222-222222222222
    scopes: [openid, profile, email, offline_access, "api://11111111-1111-1111-1111-111111111111/access"]
```

## Auth0

### Issuer and audience

The issuer is `https://<tenant>.<region>.auth0.com/`, **with** the
trailing slash. Register an API; its identifier is the audience.
Enable *Allow Offline Access* on it so the UI gets refresh tokens.

### UI client

Create a regular web application with the callback URL
`https://guardian.example.com/auth/callback` and the logout URL
`https://guardian.example.com/`. Auth0 issues an opaque access token
unless the authorize request names the API, so set
`ui.oidc.authorizationParams.audience` to the API identifier. Turn on
RP-initiated logout for the tenant so discovery advertises an
`end_session_endpoint`.

### Groups

Auth0 puts no groups on tokens by itself. Add a post-login Action that
sets a namespaced claim on the access token, for example
`https://example.com/groups`, and name it in `api.auth.groupsClaim`.

### Machine clients

Auth0 sets `azp` on client-credentials tokens, the default client
claim.

### Chart values

```yaml
api:
  auth:
    issuer: https://example.us.auth0.com/
    audience: https://repo-guardian-api
    groupsClaim: https://example.com/groups
ui:
  oidc:
    authorizationParams:
      audience: https://repo-guardian-api
```

## Google and other providers

Google's access tokens are opaque, so the API cannot verify them. Put a
broker that issues JWT access tokens in front of Google (Keycloak, Dex,
Okta or Auth0, each federating to it) and follow that broker's recipe.

For any other provider, check each line of [the contract](#the-contract):

1. Discovery works at the issuer URL, and the `issuer` it reports is
   byte for byte what you set in `api.auth.issuer`.
2. An access token for the UI client is a JWT signed RS256 or ES256
   whose `aud` contains `api.auth.audience`. If it is opaque, look for
   an `audience` or `resource` authorize parameter and set it in
   `ui.oidc.authorizationParams`.
3. The groups you want to map arrive as a top-level claim; name it in
   `api.auth.groupsClaim`.
4. Machine tokens name their client in a top-level claim; name it in
   `api.auth.clientClaim`.
5. The UI client is confidential, accepts PKCE S256, issues refresh
   tokens for the requested scopes, and authenticates with one of the
   two supported methods.
6. The redirect and post-logout URIs are registered.

## Verifying a provider

1. Sign in through the UI.
2. Open `/api/v1/me` in the same browser. It returns what the API
   resolved from your token: `subject`, `name`, `client` (absent for a
   user), and the orgs you may see (`all_orgs`, `orgs`). Groups are
   not echoed; the orgs are what they resolved to.
3. Confirm `name` is the claim you expect, and that the visible orgs
   are the union of your groups' grants. An empty list with a 403 on
   other pages means no group matched: compare the claim's values with
   the `api.authz.groups` keys.
4. Repeat with a user in a different group, and with a machine client
   if you map one under `api.authz.clients`.

A refused token answers 401 and increments
`repo_guardian_api_auth_failures_total{reason}`:

| `reason` | Meaning | Fix |
| --- | --- | --- |
| `missing` | no `Authorization: Bearer` header | the caller sent no token; the UI always sends one for a signed-in session |
| `issuer` | `iss` is not exactly `api.auth.issuer` | the issuer step of the recipe; check a trailing slash or the v1/v2 endpoint |
| `audience` | `aud` lacks `api.auth.audience` | the audience mapper (Keycloak), the authorization server audience (Okta), the API scope in `ui.oidc.scopes` (Entra ID), `authorizationParams.audience` (Auth0) |
| `algorithm` | not RS256 or ES256 | the provider's signing key settings |
| `id_token` | an ID token was sent | the caller must send the access token |
| `signature` | not signed by a key in the issuer's JWKS | the issuer URL points at the wrong tenant or realm |
| `expired`, `not_yet_valid` | `exp` or `nbf` outside 60s of skew | clock skew on the provider or the nodes; token lifetimes |
| `malformed` | not a parseable JWT | an opaque access token: see Auth0 or the checklist above |
| `unavailable` | discovery has not succeeded yet (503) | the issuer URL is unreachable from the `api` pod |

While the API has not completed discovery it answers 503 and its
readiness is false; the `api` pod's log names the issuer it is
retrying.
