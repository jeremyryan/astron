{{/*
Expand the name of the chart.
*/}}
{{- define "gamera.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Fully qualified app name.
*/}}
{{- define "gamera.fullname" -}}
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

{{- define "gamera.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels.
*/}}
{{- define "gamera.labels" -}}
helm.sh/chart: {{ include "gamera.chart" . }}
{{ include "gamera.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/part-of: gamera
{{- end }}

{{/*
Selector labels.
*/}}
{{- define "gamera.selectorLabels" -}}
app.kubernetes.io/name: {{ include "gamera.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
ServiceAccount name.
*/}}
{{- define "gamera.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "gamera.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Container image reference.
*/}}
{{- define "gamera.image" -}}
{{- $tag := default .Chart.AppVersion .Values.image.tag -}}
{{- printf "%s:%s" .Values.image.repository $tag -}}
{{- end }}

{{/*
Name of the Secret holding Neo4J credentials (created or existing).
*/}}
{{- define "gamera.credentialsSecretName" -}}
{{- if .Values.connection.existingSecret }}
{{- .Values.connection.existingSecret }}
{{- else }}
{{- printf "%s-neo4j-auth" (include "gamera.fullname" .) }}
{{- end }}
{{- end }}

{{/*
Effective Neo4J password: when the bundled Neo4J is enabled and no explicit
connection password is given, reuse the bundled instance's password.
*/}}
{{- define "gamera.neo4jPassword" -}}
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
in-cluster address of the bundled Neo4J service.
*/}}
{{- define "gamera.neo4jUri" -}}
{{- if .Values.connection.uri }}
{{- .Values.connection.uri }}
{{- else if .Values.neo4j.enabled }}
{{- printf "neo4j://%s.%s.svc.cluster.local:7687" .Values.neo4j.neo4j.name .Release.Namespace }}
{{- else }}
{{- "" }}
{{- end }}
{{- end }}
