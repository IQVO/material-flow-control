#!/usr/bin/env bash
# Chart render assertions. Run: bash charts/material-flow-control/tests/render_test.sh (needs helm and python3 + PyYAML)
set -euo pipefail
chart_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
repo_dir="$(cd "$chart_dir/../.." && pwd)"
fail() { echo "FAIL: $*" >&2; exit 1; }
vals=(--set database.url=postgres://x --set kafka.brokers=k:9092)
render() { helm template material-flow-control "$chart_dir" "${vals[@]}" "$@"; }

deployment="$(render --show-only templates/deployment.yaml)"
configmap="$(render --show-only templates/configmap.yaml)"
# Every variable cmd/api reads is in the ConfigMap or the Secret env, and nothing else (Task 19).
# ruling P4-R18: every binary also reads what the telemetry adapter reads itself (ENVIRONMENT).
telemetry_vars="$(grep -ohE 'os\.Getenv\("[A-Z_]+"' "$repo_dir"/internal/adapters/outbound/telemetry/telemetry.go | grep -oE '"[A-Z_]+"' | tr -d '"')"
grep -qx ENVIRONMENT <<<"$telemetry_vars" || fail "the telemetry adapter no longer reads ENVIRONMENT: update this test"
read_vars="$( (grep -ohE 'r\.(must|str|num|dur|pos|hours)\("[A-Z_]+"' "$repo_dir"/cmd/api/config.go | grep -oE '"[A-Z_]+"' | tr -d '"'; echo "$telemetry_vars") | sort -u)"
[ "$(wc -l <<<"$read_vars")" -ge 23 ] || fail "found only $(wc -l <<<"$read_vars") env names in cmd/api: extend the pattern, never lower the floor"
rendered="$( (grep -oE '^  [A-Z_]+:' <<<"$configmap" | tr -d ' :'; grep -oE 'name: [A-Z_]+$' <<<"$deployment" | awk '{print $2}') | sort -u)"
diff <(echo "$read_vars") <(echo "$rendered") || fail "the api env differs from what cmd/api reads (left: read, right: rendered)"
# The layout the chart mounts is the example layout, byte for byte.
cmp -s "$chart_dir/files/flow-layout.yaml" "$repo_dir/config/flow-layout.example.yaml" || fail "files/flow-layout.yaml differs from config/flow-layout.example.yaml"
cm_file="$(mktemp)"; trap 'rm -f "$cm_file"' EXIT
printf '%s\n' "$configmap" > "$cm_file"
python3 - "$repo_dir/config/flow-layout.example.yaml" "$cm_file" <<'PY'
import sys, yaml
want = yaml.safe_load(open(sys.argv[1]))
cm = yaml.safe_load(open(sys.argv[2]))
assert yaml.safe_load(cm["data"]["flow-layout.yaml"]) == want, "ConfigMap flow-layout.yaml is not the example layout"
PY
grep -q 'replicas: 1' <<<"$deployment" || fail "api replicas != 1"
grep -q 'type: Recreate' <<<"$deployment" || fail "api strategy is not Recreate"
# Guards.
render --set replicaCount=2 >/dev/null 2>&1 && fail "replicaCount=2 rendered"
helm template material-flow-control "$chart_dir" --set kafka.brokers=k:9092 >/dev/null 2>&1 && fail "rendered without a database"
helm template material-flow-control "$chart_dir" --set database.url=postgres://x >/dev/null 2>&1 && fail "rendered without kafka.brokers"
# Default render: no mcp, a2a, route or plugin.
default="$(render)"
for kind in HTTPRoute KongPlugin; do grep -q "^kind: $kind" <<<"$default" && fail "default render has a $kind"; done
grep -q 'component: mcp' <<<"$default" && fail "default render has the mcp component"
# All components: three routes, three plugins, read-only binaries without Kafka or subsystem URLs.
all="$(helm template material-flow-control "$chart_dir" -f "$chart_dir/ci/all-components-values.yaml")"
[ "$(grep -c '^kind: HTTPRoute' <<<"$all")" = 3 ] || fail "want 3 HTTPRoutes"
[ "$(grep -c '^kind: KongPlugin' <<<"$all")" = 3 ] || fail "want 3 KongPlugins"
for c in mcp a2a; do
  d="$(helm template material-flow-control "$chart_dir" -f "$chart_dir/ci/all-components-values.yaml" --show-only "templates/$c-deployment.yaml")"
  grep -qE 'KAFKA_BROKERS|ASRS_CONTROL_URL|AMR_FLEET_CONTROL_URL' <<<"$d" && fail "$c reads Kafka or a subsystem"
  grep -q 'MFC_LAYOUT_FILE' <<<"$d" || fail "$c has no layout"
done
# carry (Task 15 review): the api's grace period covers its worst shutdown - the 10 s HTTP drain overlapping the
# consumers' close and the relay pass (Kafka writes: 10 s timeouts, 2 attempts) - plus a margin; under 30 is refused.
grace="$(grep -oE 'terminationGracePeriodSeconds: [0-9]+' <<<"$deployment" | awk '{print $2}')"
[ "${grace:-0}" -ge 40 ] || fail "api terminationGracePeriodSeconds is ${grace:-unset}, want >= 40 (30 + margin)"
render --set terminationGracePeriodSeconds=29 >/dev/null 2>&1 && fail "terminationGracePeriodSeconds=29 rendered"
# carry (Task 13 review): one api replica, never zero or two (one dispatcher, D19).
render --set replicaCount=0 >/dev/null 2>&1 && fail "replicaCount=0 rendered"
# carry (Task 20 review): mcp and a2a read their database URLs from the chart's Secret (or the existing one), the
# migrations DSN included, and set exactly the variables their cmd/ reads.
for secret in "" existing-db; do
  extra=(); [ -n "$secret" ] && extra=(--set database.url= --set database.existingSecret="$secret")
  for c in mcp a2a; do
    d_file="$(mktemp)"
    helm template material-flow-control "$chart_dir" -f "$chart_dir/ci/all-components-values.yaml" ${extra[@]+"${extra[@]}"} \
      --show-only "templates/$c-deployment.yaml" > "$d_file"
    want="$( (grep -ohE '(or|getenv)\("[A-Z0-9_]+"' "$repo_dir/cmd/$c/main.go" | grep -oE '"[A-Z0-9_]+"' | tr -d '"'; echo MIGRATIONS_DATABASE_URL
      echo "$telemetry_vars") | sort -u)"
    python3 - "$d_file" "$c" "${secret:-material-flow-control-db}" "$want" <<'PY' || { rm -f "$d_file"; fail "$c env (secret ${secret:-chart})"; }
import sys, yaml
path, comp, secret, want = sys.argv[1], sys.argv[2], sys.argv[3], sys.argv[4].split()
d = yaml.safe_load(open(path))
env = {e["name"]: e for e in d["spec"]["template"]["spec"]["containers"][0]["env"]}
assert sorted(env) == sorted(want), f"{comp} renders {sorted(env)}, cmd/{comp} reads {sorted(want)}"
for name in ("DATABASE_URL", "MIGRATIONS_DATABASE_URL"):
    ref = env[name].get("valueFrom", {}).get("secretKeyRef", {})
    assert ref.get("name") == secret and ref.get("key") == name, f"{comp} {name} is {env[name]}, want secretKeyRef {secret}/{name}"
    assert "value" not in env[name], f"{comp} {name} has a literal value"
PY
    rm -f "$d_file"
  done
done
# carry (Task 23 review): the chart reads the PLATFORM keys warehouse-infra supplies (helm-values/_platform.yaml:
# kongPlugins.cors.origin, kongPlugins.opentelemetry.tracesEndpoint, mcp/a2a.route.parentRefs), like the sibling
# charts. Rendered as ArgoCD layers it (platform file first, then the app file), the plugins carry exactly the
# platform's origin and traces endpoint and every route attaches to the platform's Gateway.
fx="$chart_dir/tests/fixtures"
p_file="$(mktemp)"
helm template material-flow-control "$chart_dir" -f "$fx/platform-values.yaml" -f "$fx/app-values.yaml" > "$p_file" \
  || { rm -f "$p_file"; fail "the platform-shaped values do not render"; }
python3 - "$fx/platform-values.yaml" "$p_file" <<'PY' || { rm -f "$p_file"; fail "platform-shaped render"; }
import sys, yaml
platform = yaml.safe_load(open(sys.argv[1]))
docs = [d for d in yaml.safe_load_all(open(sys.argv[2])) if d]
plugins = {d["plugin"]: d for d in docs if d.get("kind") == "KongPlugin"}
assert sorted(plugins) == ["cors", "opentelemetry", "prometheus"], f"KongPlugins {sorted(plugins)}"
origin = platform["kongPlugins"]["cors"]["origin"]
assert plugins["cors"]["config"]["origins"] == [origin], f"cors origins {plugins['cors']['config']['origins']}, want [{origin!r}]"
traces = platform["kongPlugins"]["opentelemetry"]["tracesEndpoint"]
got = plugins["opentelemetry"]["config"]["traces_endpoint"]
assert got == traces, f"traces_endpoint {got!r}, want {traces!r}"
routes = {d["metadata"]["name"]: d for d in docs if d.get("kind") == "HTTPRoute"}
want_refs = {"material-flow-control": platform["gatewayApi"]["parentRefs"],
             "material-flow-control-mcp": platform["mcp"]["route"]["parentRefs"],
             "material-flow-control-a2a": platform["a2a"]["route"]["parentRefs"]}
assert sorted(routes) == sorted(want_refs), f"HTTPRoutes {sorted(routes)}"
for name, refs in want_refs.items():
    assert routes[name]["spec"]["parentRefs"] == refs, f"{name} parentRefs {routes[name]['spec']['parentRefs']}, want {refs}"
PY
rm -f "$p_file"
# The guards: an enabled cors plugin needs an origin (never "*"), an enabled opentelemetry plugin a traces endpoint.
render --set kongPlugins.cors.enabled=true >/dev/null 2>&1 && fail "cors enabled without an origin rendered"
render --set kongPlugins.cors.enabled=true --set kongPlugins.cors.origin='*' >/dev/null 2>&1 && fail "cors origin '*' rendered"
render --set kongPlugins.opentelemetry.enabled=true >/dev/null 2>&1 && fail "opentelemetry enabled without tracesEndpoint rendered"
# An origins list is accepted too (with or without origin), never with "*" in it.
cors="$(render --set kongPlugins.cors.enabled=true --set 'kongPlugins.cors.origins={http://a.invalid,http://b.invalid}' \
  --show-only templates/kong-plugins.yaml)"
python3 -c 'import sys, yaml; o = yaml.safe_load(sys.stdin)["config"]["origins"]; assert o == ["http://a.invalid", "http://b.invalid"], o' \
  <<<"$cors" || fail "cors origins list"
render --set kongPlugins.cors.enabled=true --set 'kongPlugins.cors.origins={http://a.invalid,*}' >/dev/null 2>&1 \
  && fail "cors origins list with '*' rendered"
# ruling P4-R18: ENVIRONMENT (deployment.environment.name on every span and metric) comes from the value
# `environment` (asrs-control's key and default) for api, mcp and a2a, so the cluster never reports "local".
env_of() { # <component> <values...>: the ENVIRONMENT the component's container gets
  local comp="$1"; shift
  if [ "$comp" = api ]; then
    helm template material-flow-control "$chart_dir" -f "$chart_dir/ci/all-components-values.yaml" "$@" --show-only templates/configmap.yaml \
      | python3 -c 'import sys, yaml; print(yaml.safe_load(sys.stdin)["data"].get("ENVIRONMENT", "<unset>"))'
  else
    helm template material-flow-control "$chart_dir" -f "$chart_dir/ci/all-components-values.yaml" "$@" --show-only "templates/$comp-deployment.yaml" \
      | python3 -c 'import sys, yaml; d = yaml.safe_load(sys.stdin); print(next((e.get("value") for e in d["spec"]["template"]["spec"]["containers"][0]["env"] if e["name"] == "ENVIRONMENT"), "<unset>"))'
  fi
}
for c in api mcp a2a; do
  got="$(env_of "$c")"; [ "$got" = production ] || fail "$c ENVIRONMENT is '$got' by default, want 'production'"
  got="$(env_of "$c" --set environment=staging)"; [ "$got" = staging ] || fail "$c ENVIRONMENT is '$got' with environment=staging"
done
echo "render_test: OK"
