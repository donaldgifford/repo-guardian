# The business UI

The UI (DESIGN-0027) shows compliance to people who are not operators:
where the fleet stands, which rules fail and where, and what
repo-guardian is doing about it. It is read-only and shows the API's
data scoped to your groups. Operators keep Grafana for the service
itself.

<!--toc:start-->
- [Enabling it](#enabling-it)
- [Views](#views)
- [Sessions](#sessions)
- [Rotating session keys](#rotating-session-keys)
- [Security properties](#security-properties)
<!--toc:end-->

## Enabling it

The UI needs the API with authentication on (see [the API](api.md)):

```yaml
api:
  enabled: true
  auth:
    issuer: https://keycloak.example.com/realms/platform
  authz:
    groups:
      team-web: ["acme"]
ui:
  enabled: true
  existingSecret: repo-guardian-ui
  ingress:
    enabled: true
    className: nginx
    host: guardian.example.com
    tls:
      - hosts: [guardian.example.com]
        secretName: guardian-tls
```

The Secret holds the OIDC client secret and the session keys:

```bash
kubectl -n repo-guardian create secret generic repo-guardian-ui \
  --from-literal=oidc-client-secret="$CLIENT_SECRET" \
  --from-literal=session-keys="$(openssl rand -base64 48)"
```

The UI signs users in against `api.auth.issuer` with the confidential
client `ui.oidc.clientId` (default `repo-guardian-ui`). The Keycloak
setup is in [the API docs](api.md#keycloak-example). The redirect URI
is `<public URL>/auth/callback`, where the public URL is `ui.publicUrl`
or `https://<ui.ingress.host>`.

The Ingress is the only one the chart renders, and it sends the whole
host to the UI. The webhook route stays yours; see
[webhook ingress](../operations/ingress.md#the-ui-host-chart-20).

## Views

The org selector in the header filters every view. It lives in the URL,
so a filtered view can be shared as a link.

| View | Path | Shows |
| --- | --- | --- |
| Fleet | `/` | tracked, compliant, failing and parked counts; the 90-day compliance trend; open PRs by age; the worst rules; parked repositories by reason |
| Rules | `/rules`, `/rules/<kind>/<name>` | compliance per rule with its change since the last snapshot; one rule by org, its top reasons, its longest failures |
| Orgs | `/orgs`, `/orgs/<org>` | compliance per org; one org's rules and why rules did not apply |
| Findings | `/findings` | every finding, filtered by status, reason, remediation, kind, rule and stale PR, with CSV export of the loaded rows |
| Repository | `/repos/<id>` | each rule's status with its evidence and PR, the timeline, and recent checks |
| History | `/history` | compliance over time per org, for all rules or one |
| Policy | `/policy` | the enforced policy version, its rollout and its rules |
| Status | `/status` | public service status: no login, no names |

Compliance shows **unmeasured** when there is nothing to measure, never
100%. A finding carried over from v1 reads "last checked by v1; details
on next check" until its repository's next check.

A signed-in user whose groups map to no org sees an "ask for access"
page with their name and subject, to quote to an administrator.

## Sessions

- The session lives only in an encrypted, `HttpOnly`, `Secure`,
  `SameSite=Lax` cookie (`__Host-rg_session`), split into chunks when
  large. Page scripts cannot read it, and the browser never sees an
  access or refresh token.
- The UI's server refreshes the access token when it is within 60
  seconds of expiry. A failed refresh signs you out.
- A session lasts at most `ui.sessionTTL` (default `8h`), however long
  the IdP's refresh token lives. Then you sign in again.
- **Sign out** revokes the refresh token at the IdP (when it supports
  RFC 7009), clears the cookie and ends the IdP session.

There is no server-side session store. A copied cookie therefore works
until it expires. The 8h cap, revocation on sign-out and key rotation
bound that.

## Rotating session keys

`session-keys` is a comma-separated list. Each key must be at least 32
bytes. The **first** key seals new sessions, and **any** listed key
opens existing ones.

- **Routine rotation, no one signed out:** prepend a new key, keeping
  the old one:
  `session-keys: <new>,<old>`. Roll the UI. After `ui.sessionTTL` has
  passed, remove `<old>`.
- **Emergency (a key leaked):** replace the list with a fresh key alone
  and roll the UI. Every session is invalid at once and everyone signs
  in again.

```bash
kubectl -n repo-guardian create secret generic repo-guardian-ui \
  --from-literal=oidc-client-secret="$CLIENT_SECRET" \
  --from-literal=session-keys="$(openssl rand -base64 48),$OLD_KEY" \
  --dry-run=client -o yaml | kubectl apply -f -
kubectl -n repo-guardian rollout restart deploy/repo-guardian-ui
```

## Security properties

- Text from repositories (assertion messages, paths, PR titles) is
  rendered as text only. There is no markdown rendering of evidence,
  and a lint rule bans every HTML sink in the UI's code.
- PR and repository links are built from the host (`ui.githubHost`),
  org, name and number, never copied from evidence, and open with
  `rel="noopener noreferrer"`.
- Every response carries `Content-Security-Policy: default-src 'self';
  connect-src 'self'; frame-ancestors 'none'; script-src 'self'`, plus
  `nosniff` and `Referrer-Policy: same-origin`.
- `/api/*` on the UI host forwards only `GET` and `HEAD`, only to the
  `api` Service. It drops your cookies before forwarding and sends
  exactly one token: yours if you sent a Bearer, otherwise the
  session's.
- The UI pod holds no GitHub, Temporal or database credential.
