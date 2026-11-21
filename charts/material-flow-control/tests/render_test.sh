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
read_vars="$(grep -ohE 'r\.(must|str|num|dur|pos)\("[A-Z_]+"' "$repo_dir"/cmd/api/config.go | grep -oE '"[A-Z_]+"' | tr -d '"' | sort -u)"
[ "$(wc -l <<<"$read_vars")" -ge 22 ] || fail "found only $(wc -l <<<"$read_vars") env names in cmd/api: extend the pattern, never lower the floor"
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
echo "render_test: OK"
