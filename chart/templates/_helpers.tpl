{{/*
Expand the name of the chart.
*/}}
{{- define "s3-router.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "s3-router.fullname" -}}
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
{{- define "s3-router.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "s3-router.labels" -}}
helm.sh/chart: {{ include "s3-router.chart" . }}
{{ include "s3-router.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "s3-router.selectorLabels" -}}
app.kubernetes.io/name: {{ include "s3-router.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "s3-router.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "s3-router.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Check if any backend uses inline credentials
*/}}
{{- define "s3-router.hasInlineCredentials" -}}
{{- $hasInline := false -}}
{{- range $backendName, $backendConfig := .Values.config.backends -}}
{{- if $backendConfig.credentials -}}
{{- if eq (default "" $backendConfig.credentials.type) "inline" -}}
{{- $hasInline = true -}}
{{- end -}}
{{- end -}}
{{- end -}}
{{- $hasInline -}}
{{- end }}
