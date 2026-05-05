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
GITHUB_APP_ID GITHUB_WEBHOOK_SECRET GITHUB_PRIVATE_KEY GITHUB_PRIVATE_KEY_PATH LISTEN_ADDR METRICS_ADDR LOG_LEVEL DRY_RUN WORKER_COUNT QUEUE_SIZE SCHEDULE_INTERVAL SKIP_FORKS SKIP_ARCHIVED GITHUB_ORG TEMPLATE_DIR WEBHOOK_IP_ALLOWLIST WEBHOOK_IP_ALLOWLIST_FAIL_OPEN TRUST_PROXY_HEADERS GUARDIAN_CONFIG STRICT_TEMPLATES
{{- end }}

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
