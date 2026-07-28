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
Selector labels that match ONLY the Janus server pods.

The bundled evaluation Postgres carries the same name+instance labels, so the
plain selectorLabels match it too. That made `kubectl port-forward svc/janus`
pick the Postgres pod and fail — the exact command NOTES.txt gives for the
unseal step — and left the database as a (port-less) endpoint of the API
Service, one label away from receiving live API traffic.

The Deployment's own .spec.selector stays broad on purpose: it is immutable,
so narrowing it would break `helm upgrade` on an existing release. Pod labels
are mutable, so the component label is added there and only the Service
selector is narrowed.
*/}}
{{- define "janus.serverSelectorLabels" -}}
{{ include "janus.selectorLabels" . }}
app.kubernetes.io/component: server
{{- end }}

{{/*
Validate the seal configuration at TEMPLATE time.

Without this an unknown seal.type rendered happily into JANUS_SEAL_TYPE and the
pod failed at boot with a confusing error, and the chart's own defaults
(awskms with an empty keyArn) produced a pod that could never unseal. Failing
here turns both into an immediate, readable error.
*/}}
{{- define "janus.validateSeal" -}}
{{- $t := .Values.seal.type -}}
{{- if not (has $t (list "shamir" "awskms" "gcpkms" "azurekv")) -}}
{{- fail (printf "seal.type must be one of shamir|awskms|gcpkms|azurekv, got %q" $t) -}}
{{- end -}}
{{- if and (eq $t "awskms") (not .Values.seal.awskms.keyArn) -}}
{{- fail "seal.type=awskms requires seal.awskms.keyArn (or use seal.type=shamir)" -}}
{{- end -}}
{{- if and (eq $t "gcpkms") (not .Values.seal.gcpkms.key) -}}
{{- fail "seal.type=gcpkms requires seal.gcpkms.key (or use seal.type=shamir)" -}}
{{- end -}}
{{- if eq $t "azurekv" -}}
{{- if or (not .Values.seal.azurekv.vaultUrl) (not .Values.seal.azurekv.keyName) -}}
{{- fail "seal.type=azurekv requires seal.azurekv.vaultUrl and seal.azurekv.keyName (or use seal.type=shamir)" -}}
{{- end -}}
{{- end -}}
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
