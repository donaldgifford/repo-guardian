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
GITHUB_APP_ID GITHUB_WEBHOOK_SECRET GITHUB_PRIVATE_KEY GITHUB_PRIVATE_KEY_PATH LISTEN_ADDR METRICS_ADDR LOG_LEVEL DRY_RUN WORKER_COUNT QUEUE_SIZE SCHEDULE_INTERVAL SKIP_FORKS SKIP_ARCHIVED TEMPLATE_DIR WEBHOOK_IP_ALLOWLIST WEBHOOK_IP_ALLOWLIST_FAIL_OPEN TRUST_PROXY_HEADERS GUARDIAN_CONFIG STRICT_TEMPLATES STORE_BACKEND QUEUE_BACKEND SCHEDULER_BACKEND STORE_DSN STORE_POSTGRES_MAX_CONNS QUEUE_VALKEY_DSN JOB_ACK_TIMEOUT REAPER_INTERVAL POD_NAME RECONCILE_FRESHNESS STALE_SWEEP_BATCH_SIZE RATE_LIMIT_RESERVE
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
Resource name for the chart-rendered Valkey deployment + Service +
PVC + Secret.
*/}}
{{- define "repo-guardian.valkeyFullname" -}}
{{- printf "%s-valkey" (include "repo-guardian.fullname" .) | trunc 63 | trimSuffix "-" -}}
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
Secret name holding QUEUE_VALKEY_DSN.
*/}}
{{- define "repo-guardian.queueSecretName" -}}
{{- if eq .Values.queue.valkey.mode "external" -}}
{{- required "queue.valkey.existingSecret is required when queue.valkey.mode=external" .Values.queue.valkey.existingSecret -}}
{{- else -}}
{{- include "repo-guardian.valkeyFullname" . -}}
{{- end -}}
{{- end -}}

{{/*
Secret key holding QUEUE_VALKEY_DSN.
*/}}
{{- define "repo-guardian.queueSecretKey" -}}
{{- if eq .Values.queue.valkey.mode "external" -}}
{{- .Values.queue.valkey.existingSecretKey | default "QUEUE_VALKEY_DSN" -}}
{{- else -}}
QUEUE_VALKEY_DSN
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
existingSecret value is consumed by exactly one store/queue mode; setting
one under any other mode used to be silently ignored, leaving the
deployment on a different credential source than the operator intended
(the chart-generated Secret), which surfaces later as auth failures.
Fail the render with an actionable message instead.

Extend this guard when adding a new mode or secret knob — the dispatch in
repo-guardian.storeSecretName / valkeySecretName must never silently drop
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
{{- if and .Values.queue.valkey.existingSecret (ne .Values.queue.valkey.mode "external") -}}
{{- fail (printf "queue.valkey.existingSecret is set but queue.valkey.mode=%s never reads it — use queue.valkey.baked.existingSecret for baked mode, or set queue.valkey.mode=external" .Values.queue.valkey.mode) -}}
{{- end -}}
{{- if and .Values.queue.valkey.baked.existingSecret (ne .Values.queue.valkey.mode "baked") -}}
{{- fail (printf "queue.valkey.baked.existingSecret is set but queue.valkey.mode=%s never reads it — use queue.valkey.existingSecret for external mode, or set queue.valkey.mode=baked" .Values.queue.valkey.mode) -}}
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
{{- if hasKey .Values.staleSweep "rateLimitReserve" -}}
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
