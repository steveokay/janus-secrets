{{/*
Expand the name of the chart.
*/}}
{{- define "janus.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Fully qualified app name.
*/}}
{{- define "janus.fullname" -}}
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

{{- define "janus.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels.
*/}}
{{- define "janus.labels" -}}
helm.sh/chart: {{ include "janus.chart" . }}
{{ include "janus.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels.
*/}}
{{- define "janus.selectorLabels" -}}
app.kubernetes.io/name: {{ include "janus.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Name of the service account to use.
*/}}
{{- define "janus.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "janus.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Name of the Secret that carries the database DSN.
When database.existingSecret is set, we use it verbatim; otherwise the chart
creates one named "<fullname>-db".
*/}}
{{- define "janus.databaseSecretName" -}}
{{- if .Values.database.existingSecret }}
{{- .Values.database.existingSecret }}
{{- else }}
{{- printf "%s-db" (include "janus.fullname" .) }}
{{- end }}
{{- end }}

{{- define "janus.databaseSecretKey" -}}
{{- default "JANUS_DATABASE_URL" .Values.database.existingSecretKey }}
{{- end }}

{{/*
Resolve the effective DSN when the bundled eval Postgres is enabled and the
user hasn't supplied their own database config. Returns "" otherwise.
*/}}
{{- define "janus.bundledDatabaseUrl" -}}
{{- if and .Values.postgresql.enabled (not .Values.database.existingSecret) (not .Values.database.url) -}}
{{- $pg := .Values.postgresql.auth -}}
{{- printf "postgres://%s:%s@%s-postgresql:5432/%s?sslmode=disable" $pg.username $pg.password (include "janus.fullname" .) $pg.database -}}
{{- end -}}
{{- end }}

{{/*
Whether the chart must render its own DSN Secret (i.e. no existingSecret was
provided but we do have a url — either explicit or from bundled Postgres).
*/}}
{{- define "janus.createDatabaseSecret" -}}
{{- if .Values.database.existingSecret -}}
false
{{- else if or .Values.database.url (include "janus.bundledDatabaseUrl" .) -}}
true
{{- else -}}
false
{{- end -}}
{{- end }}

{{/*
Whether the chart must render a metrics-token Secret.
*/}}
{{- define "janus.createMetricsSecret" -}}
{{- if and .Values.metrics.token (not .Values.metrics.existingSecret) -}}
true
{{- else -}}
false
{{- end -}}
{{- end }}

{{- define "janus.metricsSecretName" -}}
{{- if .Values.metrics.existingSecret }}
{{- .Values.metrics.existingSecret }}
{{- else }}
{{- printf "%s-metrics" (include "janus.fullname" .) }}
{{- end }}
{{- end }}
