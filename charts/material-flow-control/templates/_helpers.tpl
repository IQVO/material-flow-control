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

{{/* agents.colocate (warehouse-infra ADR 0012): "true" iff colocate, mcp and a2a are all on; else empty. */}}
{{- define "material-flow-control.agentsColocated" -}}
{{- if and (.Values.agents | default dict).colocate .Values.mcp.enabled .Values.a2a.enabled }}true{{ end -}}
{{- end }}

{{- define "material-flow-control.agentsFullname" -}}
{{- printf "%s-agents" (include "material-flow-control.fullname" .) | trunc 63 | trimSuffix "-" }}
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
{{- /* carry (Task 15 review): the drain, the consumers' close and a relay pass need about 30 s. */ -}}
{{- if lt (int .Values.terminationGracePeriodSeconds) 30 -}}
{{- fail "terminationGracePeriodSeconds must be at least 30 (10 s HTTP drain overlapping the consumers' close and a relay pass)" -}}
{{- end -}}
{{- /* carry (Task 23 review): an enabled plugin needs its platform value, or Kong runs it misconfigured. */ -}}
{{- $cors := .Values.kongPlugins.cors -}}
{{- if $cors.enabled -}}
{{- $origins := include "material-flow-control.corsOrigins" . | fromJsonArray -}}
{{- if not $origins -}}
{{- fail "kongPlugins.cors.enabled needs kongPlugins.cors.origin (the exact web origin; warehouse-infra's _platform.yaml supplies it) or kongPlugins.cors.origins" -}}
{{- end -}}
{{- if has "*" $origins -}}
{{- fail "kongPlugins.cors: origin \"*\" is refused (the endpoints are unauthenticated): name the exact web origin" -}}
{{- end -}}
{{- end -}}
{{- if and .Values.kongPlugins.opentelemetry.enabled (not .Values.kongPlugins.opentelemetry.tracesEndpoint) -}}
{{- fail "kongPlugins.opentelemetry.enabled needs kongPlugins.opentelemetry.tracesEndpoint (warehouse-infra's _platform.yaml supplies it)" -}}
{{- end -}}
{{- end -}}

{{/* carry (Task 23 review): the CORS origins as a JSON list: kongPlugins.cors.origin (the platform's string key)
first, then any kongPlugins.cors.origins entries, without duplicates. */}}
{{- define "material-flow-control.corsOrigins" -}}
{{- $cors := .Values.kongPlugins.cors -}}
{{- $origins := list -}}
{{- with $cors.origin }}{{- $origins = append $origins . -}}{{- end -}}
{{- range $cors.origins }}{{- $origins = append $origins . -}}{{- end -}}
{{- $origins | uniq | toJson -}}
{{- end -}}

{{/* carry (Task 23 review): a route's parentRefs (the dict passed in), rendered under spec.parentRefs. */}}
{{- define "material-flow-control.parentRefs" -}}
{{- range . }}
- name: {{ .name }}
  {{- if .namespace }}
  namespace: {{ .namespace }}
  {{- end }}
  {{- if .sectionName }}
  sectionName: {{ .sectionName }}
  {{- end }}
{{- end }}
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
