#!/usr/bin/env bash
# Chart render assertions (gap fixes D12, D14 and D21, 2026-10-12; ADR 0010,
# 2026-10-19).
#
# The chart deploys the REAL runtime of this context (ADR 0010: Postgres and
# Kafka are the only runtime, no in-memory mode, no log sink), so the mandatory
# configuration has no silent default, no *_MODE variable is ever rendered and
# the env the binaries read is the env the chart renders.
# Run: bash charts/product-master/tests/render_test.sh   (needs helm)
set -euo pipefail

chart_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
repo_dir="$(cd "$chart_dir/../.." && pwd)"
release=product-master
# Dummy values, no password: the chart refuses to render without a database
# source and without Kafka brokers.
base=(--set database.url=postgres://u@example.invalid:5432/db --set kafka.brokers=kafka.example.invalid:9092)
analytics=(--set analytics.enabled=true
  --set analytics.database.projectorUrl=postgres://projector@example.invalid:5432/a
  --set analytics.database.reportsUrl=postgres://reports@example.invalid:5432/a)
# Every optional value set (ct lint's values file), so every env var the
# binaries can read is rendered.
everything=(-f "$chart_dir/ci/all-components-values.yaml")

fail() { echo "FAIL: $*" >&2; exit 1; }

render() { helm template "$release" "$chart_dir" "$@"; }
# render_only <template> <helm args...>
render_only() { local t="$1"; shift; render "$@" --show-only "templates/$t"; }

configmap="$(render_only configmap.yaml "${base[@]}")"
api_deployment="$(render_only deployment.yaml "${base[@]}")"
configmap_name="$(awk '/^  name:/ {print $2; exit}' <<<"$configmap")"
[[ -n "$configmap_name" ]] || fail "could not read the ConfigMap name"

env_value() { # env_value <rendered> <NAME>
  grep -A1 "name: $2\$" <<<"$1" | grep -m1 'value:' | sed -E 's/.*value: "?//; s/"$//'
}

# D14: every config.* value is rendered into the chart ConfigMap, and the api
# Deployment -- the only workload whose binary (cmd/api) reads those keys
# (HTTP_ADDR, OUTBOX_RELAY_INTERVAL, OUTBOX_MAX_ATTEMPTS, SHUTDOWN_DRAIN_DELAY,
# CORS_ALLOWED_ORIGINS, ...) -- must mount it
# with envFrom, or a value that is only in the ConfigMap silently does nothing.
grep -A3 'envFrom:' <<<"$api_deployment" | grep -q 'configMapRef:' \
  || fail "api Deployment does not mount the ConfigMap via envFrom"
grep -A3 'envFrom:' <<<"$api_deployment" | grep -q "name: $configmap_name" \
  || fail "api Deployment envFrom does not reference ConfigMap $configmap_name"

# --- ADR 0010: the mandatory configuration is rendered from values ---------------
[[ "$(env_value "$api_deployment" KAFKA_BROKERS)" == "kafka.example.invalid:9092" ]] || fail "KAFKA_BROKERS is not rendered from kafka.brokers"
[[ "$(env_value "$api_deployment" EVENT_PUBLISHER)" == "kafka" ]] || fail "EVENT_PUBLISHER must default to kafka"
grep -q '^  EVENT_PUBLISHER: "kafka"' <<<"$configmap" || fail "the ConfigMap must carry EVENT_PUBLISHER=kafka"
grep -q '^  KAFKA_BROKERS: "kafka.example.invalid:9092"' <<<"$configmap" || fail "the ConfigMap must carry KAFKA_BROKERS"
grep -A4 'name: DATABASE_URL$' <<<"$api_deployment" | grep -q 'secretKeyRef:' || fail "DATABASE_URL must come from a Secret (secretKeyRef)"
if grep -qE '^  (MIGRATIONS_)?DATABASE_URL:' <<<"$configmap"; then fail "a DSN must never land in the ConfigMap"; fi
# warehouse-infra still passes the pre-ADR-0010 switch kafka.enabled=true: it must keep rendering.
render_only deployment.yaml "${base[@]}" --set kafka.enabled=true >/dev/null || fail "kafka.enabled=true (set by warehouse-infra) must keep rendering"

# --- no in-memory / permissive / log mode, ever ----------------------------------
no_mode() { # no_mode <label> <rendered>
  if grep -qE '(name: |^  )[A-Z_]+_MODE:?$|^  [A-Z_]+_MODE: ' <<<"$2"; then fail "$1 renders a *_MODE variable; none exists in this context"; fi
  if grep -qiE 'value: "?(permissive|log)"?$' <<<"$2"; then fail "$1 renders permissive/log, which the binary rejects at boot"; fi
}
no_mode "ConfigMap" "$configmap"
no_mode "Deployment" "$api_deployment"
all_docs="$(render "${base[@]}" "${everything[@]}")"
no_mode "the full render" "$all_docs"
if grep -q 'DATABASE_URL: ""' <<<"$all_docs"; then fail "an empty DATABASE_URL must never be rendered"; fi
if grep -qi 'in-memory\|in memory' <<<"$all_docs"; then fail "the render mentions an in-memory mode"; fi

# --- probes: /healthz liveness (and startup), /readyz readiness ------------------
probe_block() { # probe_block <probeName> <rendered deployment>
  awk -v p="$1:" '$1 == p {f=1; next} f && /^          [a-zA-Z]/ {exit} f {print}' <<<"$2"
}
grep -q 'path: /healthz' <<<"$(probe_block livenessProbe "$api_deployment")" || fail "livenessProbe must hit /healthz"
grep -q 'path: /healthz' <<<"$(probe_block startupProbe "$api_deployment")" || fail "startupProbe must hit /healthz"
readiness="$(probe_block readinessProbe "$api_deployment")"
grep -q 'path: /readyz' <<<"$readiness" || fail "readinessProbe must hit /readyz"
# /readyz checks Postgres AND that a Kafka broker answers (cached 3 s, bounded 2 s): give it a sane
# delay, period, timeout and failure budget so a broker blip does not flap.
grep -q 'initialDelaySeconds: [1-9]' <<<"$readiness" || fail "readinessProbe needs an initialDelaySeconds"
grep -q 'periodSeconds: [1-9]' <<<"$readiness" || fail "readinessProbe needs a periodSeconds"
grep -q 'timeoutSeconds: [3-9]' <<<"$readiness" || fail "readinessProbe timeoutSeconds must exceed the 2 s dependency check bound"
grep -q 'failureThreshold: [3-9]' <<<"$readiness" || fail "readinessProbe needs a failureThreshold of at least 3"
probe_block livenessProbe "$api_deployment" | grep -q 'failureThreshold: [3-9]' || fail "livenessProbe needs a failureThreshold of at least 3"

# --- graceful shutdown room -------------------------------------------------------
# SHUTDOWN_DRAIN_DELAY (5s) + HTTP drain 10s + relay 10s = 25s.
grace="$(awk '/terminationGracePeriodSeconds:/ {print $2; exit}' <<<"$api_deployment")"
(( grace >= 25 )) || fail "terminationGracePeriodSeconds is $grace; want at least 25 (drain delay + 2 x 10 s bounds)"

# A tuned config.* value reaches the ConfigMap the api mounts.
tuned="$(render_only configmap.yaml "${base[@]}" --set config.outboxRelayInterval=7s --set config.outboxMaxAttempts=5)"
grep -q 'OUTBOX_RELAY_INTERVAL: "7s"' <<<"$tuned" || fail "config.outboxRelayInterval is not rendered into the ConfigMap"
grep -q 'OUTBOX_MAX_ATTEMPTS: "5"' <<<"$tuned" || fail "config.outboxMaxAttempts is not rendered into the ConfigMap"

# D12 / ADR 0007: the relay attempt cap defaults to 20 and 0 (unlimited) is
# rendered as is.
grep -q 'OUTBOX_MAX_ATTEMPTS: "20"' <<<"$configmap" || fail "OUTBOX_MAX_ATTEMPTS must default to 20 (ADR 0007)"
grep -A1 'name: OUTBOX_MAX_ATTEMPTS' <<<"$api_deployment" | grep -q 'value: "20"' \
  || fail "api Deployment does not set OUTBOX_MAX_ATTEMPTS"
unlimited="$(render_only configmap.yaml "${base[@]}" --set config.outboxMaxAttempts=0)"
grep -q 'OUTBOX_MAX_ATTEMPTS: "0"' <<<"$unlimited" || fail "config.outboxMaxAttempts=0 is not rendered"

# The other workloads read their own explicit env and must NOT inherit the
# api's ConfigMap (reports and projector would bind the api's HTTP_ADDR).
for template in reports-deployment.yaml projector-deployment.yaml mcp-deployment.yaml; do
  if render_only "$template" "${base[@]}" "${analytics[@]}" --set mcp.enabled=true | grep -q 'configMapRef:'; then
    fail "$template must not mount the api ConfigMap"
  fi
done

# D21 / ADR 0008: every backend Deployment that sets up telemetry gets
# ENVIRONMENT (deployment.environment.name) and SERVICE_VERSION, and the OTLP
# endpoint with otel.enabled; the projector now calls telemetry.Setup too.
for template in deployment.yaml mcp-deployment.yaml projector-deployment.yaml reports-deployment.yaml; do
  rendered="$(render_only "$template" "${base[@]}" "${analytics[@]}" --set mcp.enabled=true --set environment=staging)"
  grep -A1 'name: ENVIRONMENT' <<<"$rendered" | grep -q 'value: "staging"' || fail "$template does not set ENVIRONMENT from .Values.environment"
  grep -q 'name: SERVICE_VERSION' <<<"$rendered" || fail "$template does not set SERVICE_VERSION"
  grep -q 'name: OTEL_EXPORTER_OTLP_ENDPOINT' <<<"$rendered" || fail "$template does not set OTEL_EXPORTER_OTLP_ENDPOINT with otel.enabled"
done

# --- refusals: the mandatory values name themselves --------------------------------
expect_refusal() { # expect_refusal <why> <needle> <helm args...>
  local why="$1" needle="$2" err; shift 2
  if err="$(helm template "$release" "$chart_dir" "$@" 2>&1)"; then
    fail "the chart must refuse to render: $why"
  fi
  grep -qF -- "$needle" <<<"$err" || fail "the refusal for '$why' does not name '$needle':
$err"
}
expect_refusal "no database source and no brokers" "database.url or database.existingSecret"
expect_refusal "no database source" "database.url or database.existingSecret" --set kafka.brokers=k:9092
expect_refusal "no brokers" "kafka.brokers" --set database.url=postgres://u@h/db
expect_refusal "the log publisher" "config.eventPublisher" "${base[@]}" --set config.eventPublisher=log
expect_refusal "an unknown publisher" "the only publisher" "${base[@]}" --set config.eventPublisher=rabbit
expect_refusal "kafka switched off" "Kafka cannot be switched off" "${base[@]}" --set kafka.enabled=false
expect_refusal "a short grace period" "terminationGracePeriodSeconds" "${base[@]}" --set terminationGracePeriodSeconds=10
expect_refusal "a *_MODE variable through extraEnv" "*_MODE" "${base[@]}" --set 'extraEnv[0].name=LAYOUT_MODE' --set 'extraEnv[0].value=permissive'
expect_refusal "analytics without a DSN source" "analytics.database.projectorUrl" "${base[@]}" --set analytics.enabled=true
expect_refusal "analytics without a consumer group" "analytics.projector.consumerGroup" "${base[@]}" "${analytics[@]}" --set analytics.projector.consumerGroup=

# --- chart vs binary ---------------------------------------------------------------
# A binary's env literals (os.Getenv / getenv("...")) -- for cmd/<dir> -- must all be
# rendered by its Deployment, so a new variable fails until the chart carries it.
# MIGRATIONS_DATABASE_URL is read by cmd/api and cmd/mcp through getenv(...) too.
cmd_env() { # cmd_env <dir>...
  local d f
  for d in "$@"; do
    for f in "$repo_dir/cmd/$d"/*.go; do
      case "$f" in *_test.go) continue ;; esac
      grep -hoE '(os\.Getenv|getenv)\("[A-Z][A-Z0-9_]*"' "$f" || true
    done
  done | sed -E 's/.*"([A-Z0-9_]+)".*/\1/' | sort -u
}
assert_renders_env() { # assert_renders_env <label> <rendered deployment> <dir>...
  local label="$1" rendered="$2" var; shift 2
  local vars; vars="$(cmd_env "$@")"
  [[ -n "$vars" ]] || fail "found no env literals in cmd/$* ($label); the scan is looking in the wrong place"
  for var in $vars; do
    grep -q "name: $var\$" <<<"$rendered" || fail "the $label Deployment does not render $var, which cmd/$* reads"
  done
}
api_full="$(render_only deployment.yaml "${everything[@]}")"
assert_renders_env "api" "$api_full" api
# Every key the ConfigMap renders is a variable a binary reads (no dead keys).
# CORS_ALLOWED_ORIGINS is read by the HTTP adapter and ENVIRONMENT by the
# telemetry adapter, both inside cmd/api's process.
full_configmap="$(render_only configmap.yaml "${everything[@]}")"
known=" $(cmd_env api | tr '\n' ' ') CORS_ALLOWED_ORIGINS ENVIRONMENT "
while read -r key; do
  [[ "$known" == *" $key "* ]] || fail "ConfigMap key $key is read by no binary"
done < <(awk '/^data:/ {d=1; next} d && /^  [A-Z]/ {sub(/:.*/, ""); sub(/^  /, ""); print}' <<<"$full_configmap")
assert_renders_env "mcp" "$(render_only mcp-deployment.yaml "${everything[@]}")" mcp
assert_renders_env "projector" "$(render_only projector-deployment.yaml "${everything[@]}")" product-projector
assert_renders_env "reports" "$(render_only reports-deployment.yaml "${everything[@]}")" product-reports

# The projector's consumer group and brokers come from values; the MCP binary is
# read-only and never gets Kafka env.
proj="$(render_only projector-deployment.yaml "${base[@]}" "${analytics[@]}")"
[[ "$(env_value "$proj" ANALYTICS_CONSUMER_GROUP)" == "product-master-analytics" ]] || fail "ANALYTICS_CONSUMER_GROUP is not rendered from analytics.projector.consumerGroup"
[[ "$(env_value "$proj" KAFKA_BROKERS)" == "kafka.example.invalid:9092" ]] || fail "the projector KAFKA_BROKERS is not rendered from kafka.brokers"
renamed="$(render_only projector-deployment.yaml "${base[@]}" "${analytics[@]}" --set analytics.projector.consumerGroup=g-analytics)"
[[ "$(env_value "$renamed" ANALYTICS_CONSUMER_GROUP)" == "g-analytics" ]] || fail "analytics.projector.consumerGroup does not reach ANALYTICS_CONSUMER_GROUP"
mcp_dep="$(render_only mcp-deployment.yaml "${base[@]}" --set mcp.enabled=true)"
grep -A4 'name: DATABASE_URL$' <<<"$mcp_dep" | grep -q 'secretKeyRef:' || fail "the mcp DATABASE_URL must come from a Secret"
if grep -Eq 'name: (KAFKA_BROKERS|EVENT_PUBLISHER|OUTBOX_[A-Z_]+|[A-Z]+_CONSUMER_GROUP)$' <<<"$mcp_dep"; then
  fail "the mcp Deployment must not get Kafka/relay/consumer env (read-only, never dials Kafka)"
fi

echo "PASS: product-master chart render assertions"
