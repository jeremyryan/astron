{{/*
Expand the name of the chart.
*/}}
{{- define "astron.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Fully qualified app name.
*/}}
{{- define "astron.fullname" -}}
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

{{- define "astron.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels.
*/}}
{{- define "astron.labels" -}}
helm.sh/chart: {{ include "astron.chart" . }}
{{ include "astron.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/part-of: astron
{{- end }}

{{/*
Selector labels.
*/}}
{{- define "astron.selectorLabels" -}}
app.kubernetes.io/name: {{ include "astron.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
ServiceAccount name.
*/}}
{{- define "astron.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "astron.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Container image reference.
*/}}
{{- define "astron.image" -}}
{{- $tag := default .Chart.AppVersion .Values.image.tag -}}
{{- printf "%s:%s" .Values.image.repository $tag -}}
{{- end }}

{{/*
Name of the Secret holding Neo4J credentials (created or existing).
*/}}
{{/* Distinct from the bundled Neo4J subchart's own "<name>-auth" secret. */}}
{{- define "astron.credentialsSecretName" -}}
{{- if .Values.connection.existingSecret }}
{{- .Values.connection.existingSecret }}
{{- else }}
{{- printf "%s-neo4j-credentials" (include "astron.fullname" .) }}
{{- end }}
{{- end }}

{{/*
Effective Neo4J password: when the bundled Neo4J is enabled and no explicit
connection password is given, reuse the bundled instance's password.
*/}}
{{- define "astron.neo4jPassword" -}}
{{- if .Values.connection.password }}
{{- .Values.connection.password }}
{{- else if .Values.neo4j.enabled }}
{{- .Values.neo4j.neo4j.password }}
{{- else }}
{{- "" }}
{{- end }}
{{- end }}

{{/*
Effective Neo4J bolt URI. Uses connection.uri when set, otherwise derives the
in-cluster address of the bundled Neo4J service. The bundled (official) Neo4J
chart names its primary ClusterIP service after the release name, not neo4j.name.
*/}}
{{- define "astron.neo4jUri" -}}
{{- if .Values.connection.uri }}
{{- .Values.connection.uri }}
{{- else if .Values.neo4j.enabled }}
{{- printf "neo4j://%s.%s.svc.cluster.local:7687" .Release.Name .Release.Namespace }}
{{- end }}
{{- end }}

{{/*
Name of the ConfigMap holding default configuration for new projections.
*/}}
{{- define "astron.projectionDefaultsName" -}}
{{- default (printf "%s-projection-defaults" (include "astron.fullname" .)) .Values.projectionDefaults.name }}
{{- end }}
