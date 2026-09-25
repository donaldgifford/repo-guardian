{{/*
Expand the name of the chart.
*/}}
{{- define "repo-guardian.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
We truncate at 63 chars because some Kubernetes name fields are limited to this
(by the DNS naming spec). If release name contains chart name it will be used
as a full name.
*/}}
{{- define "repo-guardian.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{/*
Create chart name and version as used by the chart label.
*/}}
{{- define "repo-guardian.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels.
*/}}
{{- define "repo-guardian.labels" -}}
helm.sh/chart: {{ include "repo-guardian.chart" . }}
{{ include "repo-guardian.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels.
*/}}
{{- define "repo-guardian.selectorLabels" -}}
app.kubernetes.io/name: {{ include "repo-guardian.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Create the name of the service account to use.
*/}}
{{- define "repo-guardian.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "repo-guardian.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Create the name of the secret to use.
*/}}
{{- define "repo-guardian.secretName" -}}
{{- if .Values.secrets.create }}
{{- include "repo-guardian.fullname" . }}
{{- else }}
{{- required "secrets.existingSecret is required when secrets.create is false" .Values.secrets.existingSecret }}
{{- end }}
{{- end }}

{{/*
Reserved env-var names — keys that the chart already manages on the
Deployment container env list. `templating.vars` may not redeclare any
of these because the chart-emitted entry would shadow the operator's
attempt and produce confusing behavior at runtime.

Returns a space-separated string for has-element style checks.
*/}}
{{- define "repo-guardian.reservedEnvVars" -}}
GITHUB_APP_ID GITHUB_WEBHOOK_SECRET GITHUB_PRIVATE_KEY GITHUB_PRIVATE_KEY_PATH LISTEN_ADDR METRICS_ADDR LOG_LEVEL DRY_RUN SKIP_FORKS SKIP_ARCHIVED AUTO_CLOSE_PR ORPHAN_CLEANUP TEMPLATE_DIR GUARDIAN_CONFIG STRICT_TEMPLATES TEMPORAL_ADDRESS TEMPORAL_NAMESPACE TEMPORAL_TASK_QUEUE TEMPORAL_TLS_CERT_PATH TEMPORAL_TLS_KEY_PATH TEMPORAL_TLS_CA_PATH TEMPORAL_TLS_SERVER_NAME WORKER_ACTIVITY_CONCURRENCY CHECK_INTERVAL POLICY_ROLLOUT_WINDOW CHECKS_RETENTION DISCOVERY_ENABLED DISCOVERY_INTERVAL COMPLIANCE_SNAPSHOT_INTERVAL STORE_DSN STORE_POSTGRES_MAX_CONNS POSTGRES_PASSWORD STORE_RO_DSN RO_PASSWORD RECONCILE_FRESHNESS API_LISTEN_ADDR API_AUTH_ENABLED OIDC_ISSUER OIDC_AUDIENCE OIDC_NAME_CLAIM OIDC_GROUPS_CLAIM API_AUTHZ_CONFIG PR_STALE_AFTER STATUS_PUBLIC
{{- end }}

{{/*
Resource name for the chart-rendered Postgres deployment + Service +
PVC + Secret. Always derived from the release fullname; the chart
does not honour an existingSecret for the *baked* mode (the existing
secret is the operator's signal to use external mode).
*/}}
{{- define "repo-guardian.postgresFullname" -}}
{{- printf "%s-postgres" (include "repo-guardian.fullname" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Secret name holding STORE_DSN. Three modes:
  - `external`: operator's existingSecret (required).
  - `baked`:    chart-rendered Secret (postgresFullname).
  - `cnpg`:     CNPG-created `<cluster>-app` Secret.
*/}}
{{- define "repo-guardian.storeSecretName" -}}
{{- if eq .Values.store.postgres.mode "external" -}}
{{- required "store.postgres.existingSecret is required when store.postgres.mode=external" .Values.store.postgres.existingSecret -}}
{{- else if eq .Values.store.postgres.mode "cnpg" -}}
{{- printf "%s-app" (include "repo-guardian.postgresFullname" .) -}}
{{- else -}}
{{- include "repo-guardian.postgresFullname" . -}}
{{- end -}}
{{- end -}}

{{/*
Secret key holding STORE_DSN. CNPG always writes connection strings
under the `uri` key; baked uses the chart-controlled `STORE_DSN`;
external honours `existingSecretKey`.
*/}}
{{- define "repo-guardian.storeSecretKey" -}}
{{- if eq .Values.store.postgres.mode "external" -}}
{{- .Values.store.postgres.existingSecretKey | default "STORE_DSN" -}}
{{- else if eq .Values.store.postgres.mode "cnpg" -}}
uri
{{- else -}}
STORE_DSN
{{- end -}}
{{- end -}}

{{/*
Validates that none of the keys in .Values.templating.vars collide with
chart-managed env vars. Calls `fail` with a clear list of offenders so
the helm-render step exits with a useful error instead of silently
shadowing the chart's own env entries.

Renders empty on success; failure aborts the entire template render.
*/}}
{{- define "repo-guardian.validateTemplatingVars" -}}
{{- $reserved := splitList " " (trim (include "repo-guardian.reservedEnvVars" .)) -}}
{{- $offenders := list -}}
{{- range $k, $_ := .Values.templating.vars -}}
{{- if has $k $reserved -}}
{{- $offenders = append $offenders $k -}}
{{- end -}}
{{- end -}}
{{- if $offenders -}}
{{- fail (printf "templating.vars keys collide with chart-managed env vars: %s" (join ", " $offenders)) -}}
{{- end -}}
{{- end }}

{{/*
Render-time guard for mode-scoped secret knobs (INV-0010). Each
existingSecret value is consumed by exactly one store mode; setting
one under any other mode used to be silently ignored, leaving the
deployment on a different credential source than the operator intended
(the chart-generated Secret), which surfaces later as auth failures.
Fail the render with an actionable message instead.

Extend this guard when adding a new mode or secret knob — the dispatch in
repo-guardian.storeSecretName must never silently drop
an operator-supplied secret.

Renders empty on success; failure aborts the entire template render.
*/}}
{{- define "repo-guardian.validateBackendSecrets" -}}
{{- if and .Values.store.postgres.existingSecret (ne .Values.store.postgres.mode "external") -}}
{{- fail (printf "store.postgres.existingSecret is set but store.postgres.mode=%s never reads it — use store.postgres.baked.existingSecret for baked mode, or set store.postgres.mode=external" .Values.store.postgres.mode) -}}
{{- end -}}
{{- if and .Values.store.postgres.baked.existingSecret (ne .Values.store.postgres.mode "baked") -}}
{{- fail (printf "store.postgres.baked.existingSecret is set but store.postgres.mode=%s never reads it — use store.postgres.existingSecret for external mode, or set store.postgres.mode=baked" .Values.store.postgres.mode) -}}
{{- end -}}
{{- end }}

{{/*
Fail render when a values file still sets a removed knob (IMPL-0022
Phase 6, IMPL-0024, chart 2.0.0). JSON Schema accepts unknown keys (there is no
additionalProperties: false on this chart), so without this guard a
stale values file renders happily and the operator silently loses
the behaviour they think they configured. Same shape as
validateBackendSecrets — extend when a knob is removed, and delete
the entry once operators have had a release or two to notice.
*/}}
{{- define "repo-guardian.validateRemovedValues" -}}
{{- if hasKey .Values.discovery "reserveFraction" -}}
{{- fail "discovery.reserveFraction was removed in IMPL-0022: the BudgetTracker it configured is gone (it never gated anything — INV-0012 finding A). Delete the value. See docs/operations/migrations.md#removing-the-rate-limit-reserve-knobs-impl-0022" -}}
{{- end -}}
{{- if hasKey .Values.discovery "estimatedCostPerRepo" -}}
{{- fail "discovery.estimatedCostPerRepo was removed in IMPL-0022: the BudgetTracker it configured is gone. Delete the value. See docs/operations/migrations.md#removing-the-rate-limit-reserve-knobs-impl-0022" -}}
{{- end -}}
{{- /* Chart 2.0.0 (IMPL-0025): the v1 queue runtime is gone. */ -}}
{{- range $k := list "queue" "scheduler" "staleSweep" -}}
{{- if hasKey $.Values $k -}}
{{- fail (printf "%s.* was removed in chart 2.0.0: Temporal replaces the Valkey queue, scheduler and stale sweep. Delete the block. See docs/operations/v2-migration.md#removed-chart-values" $k) -}}
{{- end -}}
{{- end -}}
{{- range $k := list "workerCount" "queueSize" "scheduleInterval" "maxJobAttempts" -}}
{{- if hasKey $.Values.config $k -}}
{{- fail (printf "config.%s was removed in chart 2.0.0 (use worker.concurrency and checkInterval). Delete the value. See docs/operations/v2-migration.md#removed-chart-values" $k) -}}
{{- end -}}
{{- end -}}
{{- if hasKey (.Values.posture | default dict) "exportInterval" -}}
{{- fail "posture.exportInterval was removed in chart 2.0.0: the posture gauges are gone and the API reads compliance from Postgres. Delete the value. See docs/operations/v2-migration.md#removed-chart-values" -}}
{{- end -}}
{{- if hasKey .Values "tailscale" -}}
{{- fail "tailscale.* was removed in IMPL-0024: ingress is operator-owned (the baked sidecar also forced the IP allowlist fail-open — INV-0016). Delete the block and pick an ingress option. See docs/operations/ingress.md#migrating-from-the-baked-sidecar" -}}
{{- end -}}
{{- if hasKey .Values "webhookIPAllowlist" -}}
{{- fail "webhookIPAllowlist.* was removed in IMPL-0024: the in-app IP allowlist was spoofable behind every documented proxy and was deleted — source-IP enforcement now lives at the operator's edge layer. Delete the block. See docs/operations/ingress.md#migrating-from-the-baked-sidecar" -}}
{{- end -}}
{{- end }}

{{/*
STORE_DSN (and, for baked Postgres with an operator secret, the
POSTGRES_PASSWORD it expands) as container env entries. Shared by the
Deployment and the migrate Job so the two can never disagree on the DSN.
*/}}
{{- define "repo-guardian.storeDSNEnv" -}}
{{- if and (eq .Values.store.postgres.mode "baked") .Values.store.postgres.baked.existingSecret }}
# Baked Postgres with an operator-supplied password: assemble
# STORE_DSN at runtime from $(POSTGRES_PASSWORD) so the chart
# never needs the password at template time (GitOps-safe).
# POSTGRES_PASSWORD must precede STORE_DSN for $(...) expansion.
- name: POSTGRES_PASSWORD
  valueFrom:
    secretKeyRef:
      name: {{ .Values.store.postgres.baked.existingSecret }}
      key: {{ .Values.store.postgres.baked.existingSecretKey | default "POSTGRES_PASSWORD" }}
- name: STORE_DSN
  value: "postgres://repoguardian:$(POSTGRES_PASSWORD)@{{ include "repo-guardian.postgresFullname" . }}.{{ .Release.Namespace }}.svc.cluster.local:5432/repoguardian?sslmode=disable"
{{- else }}
- name: STORE_DSN
  valueFrom:
    secretKeyRef:
      name: {{ include "repo-guardian.storeSecretName" . }}
      key: {{ include "repo-guardian.storeSecretKey" . }}
{{- end }}
{{- end }}

{{/*
===========================================================================
v2 roles (DESIGN-0026 § Chart 2.0.0, IMPL-0025 Phase 17).

topology=split renders one Deployment per role (ingest, worker, and api
when api.enabled); topology=all renders a single Deployment running every
role. Each role helper takes a dict: {ctx: $, role: "<name>"}.
===========================================================================
*/}}

{{/*
The roles rendered for the current topology.
*/}}
{{- define "repo-guardian.roles" -}}
{{- if eq .Values.topology "all" -}}
all
{{- else -}}
ingest worker{{ if .Values.api.enabled }} api{{ end }}
{{- end -}}
{{- end }}

{{/*
Resource name for one role.
*/}}
{{- define "repo-guardian.roleFullname" -}}
{{- printf "%s-%s" (include "repo-guardian.fullname" .ctx) .role | trunc 63 | trimSuffix "-" -}}
{{- end }}

{{/*
Selector labels for one role. The component label keeps a role's
Service, PDB and ServiceMonitor off every other role's pods (and off the
baked Postgres pod, which shares the name and instance labels).
*/}}
{{- define "repo-guardian.roleSelectorLabels" -}}
{{ include "repo-guardian.selectorLabels" .ctx }}
app.kubernetes.io/component: {{ .role }}
{{- end }}

{{- define "repo-guardian.roleLabels" -}}
{{ include "repo-guardian.labels" .ctx }}
app.kubernetes.io/component: {{ .role }}
{{- end }}

{{/*
What each role holds. Secret scoping (DESIGN-0026): the App key only in
worker and all, the webhook secret only in ingest and all, Temporal in
every role that dials it, and nothing but the read-only DSN in api.
*/}}
{{- define "repo-guardian.roleHasAppKey" -}}{{ if has .role (list "worker" "all") }}true{{ end }}{{- end }}
{{- define "repo-guardian.roleHasWebhookSecret" -}}{{ if has .role (list "ingest" "all") }}true{{ end }}{{- end }}
{{- define "repo-guardian.roleDialsTemporal" -}}{{ if has .role (list "ingest" "worker" "all") }}true{{ end }}{{- end }}
{{- define "repo-guardian.roleHasStore" -}}{{ if has .role (list "worker" "all") }}true{{ end }}{{- end }}
{{- define "repo-guardian.roleServesAPI" -}}{{ if has .role (list "api" "all") }}true{{ end }}{{- end }}
{{- define "repo-guardian.roleReadsPolicy" -}}{{ if has .role (list "ingest" "worker" "all") }}true{{ end }}{{- end }}

{{/*
Whether a policy file is mounted.
*/}}
{{- define "repo-guardian.hasPolicy" -}}
{{- if or .Values.policy.config .Values.policy.existingConfigMap }}true{{ end -}}
{{- end }}

{{/*
The API's listen port: the main port when the api role runs alone, the
sidecar port beside other roles (all).
*/}}
{{- define "repo-guardian.apiPort" -}}
{{- if eq .role "all" }}{{ .ctx.Values.api.port }}{{ else }}{{ .ctx.Values.config.port }}{{ end -}}
{{- end }}

{{/*
Temporal connection env (TEMPORAL_*) and, with mTLS, the mounted paths.
*/}}
{{- define "repo-guardian.temporalEnv" -}}
- name: TEMPORAL_ADDRESS
  value: {{ .Values.temporal.address | quote }}
- name: TEMPORAL_NAMESPACE
  value: {{ .Values.temporal.namespace | quote }}
- name: TEMPORAL_TASK_QUEUE
  value: {{ .Values.temporal.taskQueue | quote }}
{{- with .Values.temporal.tls.existingSecret }}
- name: TEMPORAL_TLS_CERT_PATH
  value: /etc/repo-guardian/temporal-tls/tls.crt
- name: TEMPORAL_TLS_KEY_PATH
  value: /etc/repo-guardian/temporal-tls/tls.key
- name: TEMPORAL_TLS_CA_PATH
  value: /etc/repo-guardian/temporal-tls/ca.crt
{{- with $.Values.temporal.tls.serverName }}
- name: TEMPORAL_TLS_SERVER_NAME
  value: {{ . | quote }}
{{- end }}
{{- end }}
{{- end }}

{{/*
The api role's env: listener, read-only DSN, auth and status.
*/}}
{{- define "repo-guardian.apiEnv" -}}
{{- $v := .ctx.Values -}}
- name: API_LISTEN_ADDR
  value: ":{{ include "repo-guardian.apiPort" . }}"
{{- if eq .role "api" }}
{{- include "repo-guardian.storeRODSNEnv" .ctx }}
{{- end }}
{{- if and $v.api.enabled $v.api.auth.enabled }}
- name: API_AUTH_ENABLED
  value: "true"
- name: OIDC_ISSUER
  value: {{ $v.api.auth.issuer | quote }}
- name: OIDC_AUDIENCE
  value: {{ $v.api.auth.audience | quote }}
- name: OIDC_NAME_CLAIM
  value: {{ $v.api.auth.nameClaim | quote }}
- name: OIDC_GROUPS_CLAIM
  value: {{ $v.api.auth.groupsClaim | quote }}
- name: API_AUTHZ_CONFIG
  value: /etc/repo-guardian/authz/authz.yaml
{{- else }}
# The api role has no Service unless api.enabled, and with auth off it is
# reachable on the pod network only (INV-0009).
- name: API_AUTH_ENABLED
  value: "false"
{{- end }}
- name: PR_STALE_AFTER
  value: {{ $v.api.prStaleAfter | quote }}
- name: STATUS_PUBLIC
  value: {{ $v.api.status.public | quote }}
{{- end }}

{{/*
STORE_RO_DSN for the api role (DESIGN-0027 § Chart). An operator Secret
wins; otherwise baked and CNPG derive it from the chart-managed
read-only role, and external has no source (a guard fails render).
*/}}
{{- define "repo-guardian.storeRODSNEnv" -}}
{{- $pg := include "repo-guardian.postgresFullname" . -}}
{{- if .Values.api.roDsn.existingSecret }}
- name: STORE_RO_DSN
  valueFrom:
    secretKeyRef:
      name: {{ .Values.api.roDsn.existingSecret }}
      key: {{ .Values.api.roDsn.existingSecretKey | default "STORE_RO_DSN" }}
{{- else if eq .Values.store.postgres.mode "baked" }}
- name: RO_PASSWORD
  valueFrom:
    secretKeyRef:
      name: {{ include "repo-guardian.roSecretName" . }}
      key: {{ include "repo-guardian.roSecretKey" . }}
- name: STORE_RO_DSN
  value: "postgres://repoguardian_ro:$(RO_PASSWORD)@{{ $pg }}.{{ .Release.Namespace }}.svc.cluster.local:5432/repoguardian?sslmode=disable"
{{- else if eq .Values.store.postgres.mode "cnpg" }}
- name: RO_PASSWORD
  valueFrom:
    secretKeyRef:
      name: {{ include "repo-guardian.roSecretName" . }}
      key: password
{{- /* CNPG's -ro Service routes to replicas only; one instance has none. */}}
- name: STORE_RO_DSN
  value: "postgres://repoguardian_ro:$(RO_PASSWORD)@{{ $pg }}-{{ if gt (int .Values.store.postgres.cnpg.instances) 1 }}ro{{ else }}rw{{ end }}.{{ .Release.Namespace }}.svc.cluster.local:5432/repoguardian?sslmode=require"
{{- end }}
{{- end }}

{{/*
Secret holding the read-only role's password: the operator's baked
Secret (key existingSecretROKey) or the chart-rendered <postgres>-ro.
*/}}
{{- define "repo-guardian.roSecretName" -}}
{{- if and (eq .Values.store.postgres.mode "baked") .Values.store.postgres.baked.existingSecret -}}
{{- .Values.store.postgres.baked.existingSecret -}}
{{- else -}}
{{- printf "%s-ro" (include "repo-guardian.postgresFullname" .) -}}
{{- end -}}
{{- end }}

{{- define "repo-guardian.roSecretKey" -}}
{{- if and (eq .Values.store.postgres.mode "baked") .Values.store.postgres.baked.existingSecret -}}
{{- .Values.store.postgres.baked.existingSecretROKey | default "RO_PASSWORD" -}}
{{- else -}}
password
{{- end -}}
{{- end }}

{{/*
Render-time guards for the v2 roles (IMPL-0025 17.6). Renders empty on
success; failure aborts the render with the fix.
*/}}
{{- define "repo-guardian.validateRoles" -}}
{{- if not .Values.temporal.address -}}
{{- fail "temporal.address is required: ingest and the worker dial Temporal. See docs/operations/v2-migration.md" -}}
{{- end -}}
{{- if not (include "repo-guardian.hasPolicy" .) -}}
{{- fail "policy.config or policy.existingConfigMap is required: the worker will not start without GUARDIAN_CONFIG" -}}
{{- end -}}
{{- if and .Values.api.enabled .Values.api.auth.enabled (not .Values.api.auth.issuer) -}}
{{- fail "api.auth.issuer is required when api.auth.enabled (or set api.auth.enabled=false for a pod-network-only API)" -}}
{{- end -}}
{{- if and (eq .Values.topology "split") .Values.api.enabled (eq .Values.store.postgres.mode "external") (not .Values.api.roDsn.existingSecret) -}}
{{- fail "api.roDsn.existingSecret is required with store.postgres.mode=external: the api role reads through a read-only role the chart cannot create on an external database" -}}
{{- end -}}
{{- end }}

{{/*
Whether the chart manages the api role's read-only Postgres role
(DESIGN-0027 § Chart): a split API with no operator DSN on a Postgres
the chart runs. In `all` the API reads with STORE_DSN.
*/}}
{{- define "repo-guardian.needsRORole" -}}
{{- if and (eq .Values.topology "split") .Values.api.enabled (not .Values.api.roDsn.existingSecret) (has .Values.store.postgres.mode (list "baked" "cnpg")) -}}
true
{{- end -}}
{{- end }}

{{/*
SQL that makes repoguardian_ro able to read every table, now and later.
Idempotent: the OQ24 hook re-runs it on every install and upgrade.
*/}}
{{- define "repo-guardian.roGrantsSQL" -}}
GRANT CONNECT ON DATABASE repoguardian TO repoguardian_ro;
GRANT USAGE ON SCHEMA public TO repoguardian_ro;
GRANT SELECT ON ALL TABLES IN SCHEMA public TO repoguardian_ro;
ALTER DEFAULT PRIVILEGES FOR ROLE repoguardian IN SCHEMA public GRANT SELECT ON TABLES TO repoguardian_ro;
{{- end }}

{{/*
PGPASSWORD for the baked Postgres admin (repoguardian), from the
operator's Secret or the chart-rendered one.
*/}}
{{- define "repo-guardian.bakedAdminPasswordEnv" -}}
- name: PGPASSWORD
  valueFrom:
    secretKeyRef:
      {{- if .Values.store.postgres.baked.existingSecret }}
      name: {{ .Values.store.postgres.baked.existingSecret }}
      key: {{ .Values.store.postgres.baked.existingSecretKey | default "POSTGRES_PASSWORD" }}
      {{- else }}
      name: {{ include "repo-guardian.postgresFullname" . }}
      key: POSTGRES_PASSWORD
      {{- end }}
{{- end }}
