{{/*
Expand the name of the chart.
*/}}
{{- define "product-master.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "product-master.fullname" -}}
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
Chart name and version as used by the chart label.
*/}}
{{- define "product-master.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "product-master.labels" -}}
helm.sh/chart: {{ include "product-master.chart" . }}
{{ include "product-master.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels. Shared by every pod this release creates, so every
Deployment and Service ALSO pins app.kubernetes.io/component (api, mcp):
a Service selecting on these two alone would select every component.
*/}}
{{- define "product-master.selectorLabels" -}}
app.kubernetes.io/name: {{ include "product-master.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "product-master.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "product-master.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Name of the Secret holding DATABASE_URL: the operator's own
(database.existingSecret) or the one this chart creates from database.url.
*/}}
{{- define "product-master.databaseSecretName" -}}
{{- if .Values.database.existingSecret }}
{{- .Values.database.existingSecret }}
{{- else }}
{{- include "product-master.fullname" . }}-database
{{- end }}
{{- end }}

{{/*
Fully qualified name of the MCP server deployment/service.
*/}}
{{- define "product-master.mcpFullname" -}}
{{- include "product-master.fullname" . }}-mcp
{{- end }}

{{/*
Image reference shared by the api and mcp Deployments.
*/}}
{{- define "product-master.image" -}}
{{ .Values.image.repository }}:{{ .Values.image.tag | default .Chart.AppVersion }}
{{- end }}

{{/*
Fails chart rendering with a clear message if no DATABASE_URL source is
configured. The binary would silently fall back to in-memory adapters (state
lost on restart) -- fine for `go run`, never a deployable state, so surface it
as a helm render-time error instead.
*/}}
{{- define "product-master.requireDatabase" -}}
{{- if not (or .Values.database.url .Values.database.existingSecret) -}}
{{- fail "product-master requires database.url or database.existingSecret to be set — without DATABASE_URL the binary silently runs on in-memory adapters, which is not a deployable state." -}}
{{- end -}}
{{- end -}}

{{/*
EVENT_PUBLISHER=kafka makes the binary refuse to boot without KAFKA_BROKERS
(cmd/api startOutboxRelay), and KAFKA_BROKERS is only rendered when
kafka.enabled is true -- so that combination would crash-loop. Fail at render.
*/}}
{{- define "product-master.requireKafkaForPublisher" -}}
{{- if and (eq .Values.config.eventPublisher "kafka") (not .Values.kafka.enabled) -}}
{{- fail "config.eventPublisher is \"kafka\" but kafka.enabled is false — the binary exits at boot (EVENT_PUBLISHER=kafka requires KAFKA_BROKERS). Set kafka.enabled=true and kafka.brokers." -}}
{{- end -}}
{{- end -}}

{{/*
LEGACY_IMPORT_CONSUMER_GROUP without KAFKA_BROKERS makes the binary refuse to
boot (cmd/api startLegacyImporter). Fail at render instead of crash-looping.
*/}}
{{- define "product-master.requireKafkaForLegacyImport" -}}
{{- if and .Values.config.legacyImportConsumerGroup (not .Values.kafka.enabled) -}}
{{- fail "config.legacyImportConsumerGroup is set but kafka.enabled is false — the binary exits at boot (LEGACY_IMPORT_CONSUMER_GROUP requires KAFKA_BROKERS). Set kafka.enabled=true and kafka.brokers." -}}
{{- end -}}
{{- end -}}
