{{/*
Expand the name of the chart.
*/}}
{{- define "pm-controller.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "pm-controller.fullname" -}}
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
{{- define "pm-controller.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "pm-controller.labels" -}}
helm.sh/chart: {{ include "pm-controller.chart" . }}
{{ include "pm-controller.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "pm-controller.selectorLabels" -}}
app.kubernetes.io/name: {{ include "pm-controller.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "pm-controller.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "pm-controller.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Alert labels injected into every PrometheusRule alert.
*/}}
{{- define "pm-controller.alertLabels" -}}
severity: {{ .Values.alerts.severity | default "info" | quote }}
service: {{ .Values.alerts.labels.service | default "permission-manager" | quote }}
{{- if .Values.alerts.labels.support_group }}
support_group: {{ .Values.alerts.labels.support_group | quote }}
{{- end }}
{{- end }}
