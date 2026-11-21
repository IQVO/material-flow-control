{{- define "material-flow-control.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "material-flow-control.fullname" -}}
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

{{/* The read-only agent components (ADR 0005): <fullname>-mcp and <fullname>-a2a. */}}
{{- define "material-flow-control.mcpFullname" -}}
{{- printf "%s-mcp" (include "material-flow-control.fullname" .) | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "material-flow-control.a2aFullname" -}}
{{- printf "%s-a2a" (include "material-flow-control.fullname" .) | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "material-flow-control.selectorLabels" -}}
app.kubernetes.io/name: {{ include "material-flow-control.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{- define "material-flow-control.labels" -}}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{ include "material-flow-control.selectorLabels" . }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{- define "material-flow-control.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "material-flow-control.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{- define "material-flow-control.image" -}}
{{- printf "%s:%s" .Values.image.repository (.Values.image.tag | default .Chart.AppVersion) }}
{{- end }}

{{/* service.version: serviceVersion, else the image tag, else appVersion. */}}
{{- define "material-flow-control.serviceVersion" -}}
{{- .Values.serviceVersion | default .Values.image.tag | default .Chart.AppVersion }}
{{- end }}

{{- define "material-flow-control.databaseSecretName" -}}
{{- default (printf "%s-db" (include "material-flow-control.fullname" .)) .Values.database.existingSecret }}
{{- end }}

{{/* Mandatory values: the service runs on real Postgres and Kafka and calls both subsystems, as one api replica. */}}
{{- define "material-flow-control.require" -}}
{{- if ne (int .Values.replicaCount) 1 -}}
{{- fail "replicaCount must be 1: the dispatcher and the consumers assume one api process (plan Global Constraints, D19)" -}}
{{- end -}}
{{- if and (not .Values.database.url) (not .Values.database.existingSecret) -}}
{{- fail "database.url or database.existingSecret is required" -}}
{{- end -}}
{{- if not .Values.kafka.brokers -}}
{{- fail "kafka.brokers is required" -}}
{{- end -}}
{{- if or (not .Values.subsystems.asrsControlUrl) (not .Values.subsystems.amrFleetControlUrl) -}}
{{- fail "subsystems.asrsControlUrl and subsystems.amrFleetControlUrl are required" -}}
{{- end -}}
{{- end -}}

{{/* The comma-separated names of the enabled KongPlugins, for each route's konghq.com/plugins annotation. */}}
{{- define "material-flow-control.kongPluginNames" -}}
{{- $names := list -}}
{{- range $name, $cfg := .Values.kongPlugins -}}
{{- if $cfg.enabled -}}
{{- $names = append $names (printf "%s-%s" (include "material-flow-control.fullname" $) $name) -}}
{{- end -}}
{{- end -}}
{{- join "," $names -}}
{{- end -}}

{{/* The port of a listen address (":8090", "0.0.0.0:8090" -> 8090): containerPort and Service port of mcp/a2a. */}}
{{- define "material-flow-control.port" -}}
{{- regexReplaceAll "^.*:" . "" -}}
{{- end -}}
