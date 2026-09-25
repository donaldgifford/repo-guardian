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
GITHUB_APP_ID GITHUB_WEBHOOK_SECRET GITHUB_PRIVATE_KEY GITHUB_PRIVATE_KEY_PATH LISTEN_ADDR METRICS_ADDR LOG_LEVEL DRY_RUN WORKER_COUNT QUEUE_SIZE SCHEDULE_INTERVAL SKIP_FORKS SKIP_ARCHIVED TEMPLATE_DIR GUARDIAN_CONFIG STRICT_TEMPLATES STORE_BACKEND QUEUE_BACKEND SCHEDULER_BACKEND STORE_DSN STORE_POSTGRES_MAX_CONNS QUEUE_VALKEY_DSN JOB_ACK_TIMEOUT REAPER_INTERVAL POD_NAME RECONCILE_FRESHNESS STALE_SWEEP_BATCH_SIZE RATE_LIMIT_RESERVE
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
Fail render when a values file still sets a knob removed in
IMPL-0022 Phase 6. JSON Schema accepts unknown keys (there is no
additionalProperties: false on this chart), so without this guard a
stale values file renders happily and the operator silently loses
the behaviour they think they configured. Same shape as
validateBackendSecrets — extend when a knob is removed, and delete
the entry once operators have had a release or two to notice.
*/}}
{{- define "repo-guardian.validateRemovedValues" -}}
{{- if hasKey (.Values.staleSweep | default dict) "rateLimitReserve" -}}
{{- fail "staleSweep.rateLimitReserve was removed in IMPL-0022: the sweep no longer gates on the rate-limit reserve — throttled work defers itself with a due-time instead. Delete the value. See docs/operations/migrations.md#removing-the-rate-limit-reserve-knobs-impl-0022" -}}
{{- end -}}
{{- if hasKey .Values.discovery "reserveFraction" -}}
{{- fail "discovery.reserveFraction was removed in IMPL-0022: the BudgetTracker it configured is gone (it never gated anything — INV-0012 finding A). Delete the value. See docs/operations/migrations.md#removing-the-rate-limit-reserve-knobs-impl-0022" -}}
{{- end -}}
{{- if hasKey .Values.discovery "estimatedCostPerRepo" -}}
{{- fail "discovery.estimatedCostPerRepo was removed in IMPL-0022: the BudgetTracker it configured is gone. Delete the value. See docs/operations/migrations.md#removing-the-rate-limit-reserve-knobs-impl-0022" -}}
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
  value: {{ required "temporal.address is required" .Values.temporal.address | quote }}
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
