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
Fully qualified name of the analytics projector deployment (ADR 0006).
*/}}
{{- define "product-master.projectorFullname" -}}
{{- include "product-master.fullname" . }}-projector
{{- end }}

{{/*
Fully qualified name of the analytics reports deployment/service (ADR 0006).
The reports Service is cluster-internal (component=analytics-reports); nothing
in this chart routes external traffic to it (warehouse-infra owns the edge).
*/}}
{{- define "product-master.reportsFullname" -}}
{{- include "product-master.fullname" . }}-reports
{{- end }}

{{/*
Name of the Secret holding the analytical DSNs: the operator's own
(analytics.database.existingSecret, keys ANALYTICS_DATABASE_URL and
ANALYTICS_READER_DATABASE_URL) or the one this chart creates.
*/}}
{{- define "product-master.analyticsSecretName" -}}
{{- if .Values.analytics.database.existingSecret }}
{{- .Values.analytics.database.existingSecret }}
{{- else }}
{{- include "product-master.fullname" . }}-analytics
{{- end }}
{{- end }}

{{/*
analytics.enabled needs an analytical DSN source (both binaries refuse to boot
without ANALYTICS_DATABASE_URL) and a stable consumer group (the projector
refuses to boot without ANALYTICS_CONSUMER_GROUP). Its broker is the one
kafka.brokers that product-master.requireKafka already makes mandatory. Fail at
render instead of crash-looping.
*/}}
{{- define "product-master.requireAnalyticsConfig" -}}
{{- if not (or .Values.analytics.database.projectorUrl .Values.analytics.database.existingSecret) -}}
{{- fail "analytics.enabled is true but neither analytics.database.projectorUrl nor analytics.database.existingSecret is set — the projector and reports binaries refuse to boot without ANALYTICS_DATABASE_URL." -}}
{{- end -}}
{{- $_ := required "analytics.enabled is true but analytics.projector.consumerGroup is empty: the projector exits at boot without ANALYTICS_CONSUMER_GROUP, a stable Kafka group id (ADR 0010)." .Values.analytics.projector.consumerGroup -}}
{{- end -}}

{{/*
Image reference shared by every Deployment (api, mcp, analytics-projector, analytics-reports).
*/}}
{{- define "product-master.image" -}}
{{ .Values.image.repository }}:{{ .Values.image.tag | default .Chart.AppVersion }}
{{- end }}

{{/*
MANDATORY (ADR 0010: Postgres is the only runtime). The binaries (cmd/api and
cmd/mcp) exit at boot without DATABASE_URL; there is no in-memory mode.
*/}}
{{- define "product-master.requireDatabase" -}}
{{- if not (or .Values.database.url .Values.database.existingSecret) -}}
{{- fail "product-master requires database.url or database.existingSecret to be set (DATABASE_URL): the service runs on real Postgres only and exits at boot without it (ADR 0010)." -}}
{{- end -}}
{{- end -}}

{{/*
MANDATORY (ADR 0010: Kafka is the only runtime). cmd/api exits at boot without
KAFKA_BROKERS and without the kafka event publisher; there is no default
broker, no log sink and no way to switch Kafka off. kafka.enabled is kept only
so values written before ADR 0010 still render: it may be true, never false.
*/}}
{{- define "product-master.requireKafka" -}}
{{- $_ := required "product-master requires kafka.brokers to be set (KAFKA_BROKERS, comma-separated): the service runs on real Kafka only and exits at boot without it (ADR 0010)." .Values.kafka.brokers -}}
{{- if and (hasKey .Values.kafka "enabled") (not .Values.kafka.enabled) -}}
{{- fail "kafka.enabled is false — Kafka cannot be switched off: the service runs on real Kafka only (ADR 0010). Drop the key or set it to true and set kafka.brokers." -}}
{{- end -}}
{{- if ne (toString .Values.config.eventPublisher) "kafka" -}}
{{- fail (printf "config.eventPublisher is %q — the only publisher is \"kafka\": the log sink was removed (ADR 0010); the binary rejects EVENT_PUBLISHER=log at boot." (toString .Values.config.eventPublisher)) -}}
{{- end -}}
{{- end -}}

{{/*
Shutdown is sequential: SHUTDOWN_DRAIN_DELAY (default 5s) + HTTP drain 10s +
outbox relay 10s = 25s in the worst case. A shorter grace period gets the pod
SIGKILLed mid-drain.
*/}}
{{- define "product-master.requireGracePeriod" -}}
{{- if lt (int .Values.terminationGracePeriodSeconds) 25 -}}
{{- fail (printf "terminationGracePeriodSeconds is %v — want at least 25: SHUTDOWN_DRAIN_DELAY (5s default) + 10s HTTP drain + 10s outbox relay." .Values.terminationGracePeriodSeconds) -}}
{{- end -}}
{{- end -}}

{{/*
No *_MODE variable exists in this context (it consumes nothing in cmd/api) and
none may be injected through extraEnv: ADR 0010 allows no permissive/off mode.
*/}}
{{- define "product-master.requireNoModeEnv" -}}
{{- range .Values.extraEnv -}}
{{- if hasSuffix "_MODE" (toString .name) -}}
{{- fail (printf "extraEnv sets %s — *_MODE variables are not configurable: product-master runs on real Postgres and real Kafka only, with no permissive or off mode (ADR 0010)." .name) -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{/*
Comma-joined names of this chart's enabled KongPlugins, for the
konghq.com/plugins annotation on every HTTPRoute this chart renders.
Empty string (annotation omitted) when none are enabled — standalone
installs without Kong are unaffected.
*/}}
{{- define "product-master.kongPluginNames" -}}
{{- $names := list -}}
{{- range $name, $cfg := .Values.kongPlugins -}}
{{- if $cfg.enabled -}}
{{- $names = append $names (printf "%s-%s" (include "product-master.fullname" $) $name) -}}
{{- end -}}
{{- end -}}
{{- join "," $names -}}
{{- end -}}
