#!/usr/bin/env python3
"""Assert every Service selects exactly one Deployment, of its own component.

`app.kubernetes.io/name` + `instance` are identical on every pod of a release, so a Service must
also pin `app.kubernetes.io/component`. Rendered three times: the defaults (the api only),
ci/all-components-values.yaml (api, mcp and a2a; ADR 0005), and the same file with agents.colocate=true
(warehouse-infra ADR 0012: the mcp and a2a Services both select the one `agents` Deployment).

Run: python3 charts/material-flow-control/tests/test_service_selectors.py
Needs: helm, PyYAML.
"""

from __future__ import annotations

import subprocess
import sys
from pathlib import Path

CHART_DIR = Path(__file__).resolve().parents[1]


def render(values: str, sets: tuple[str, ...] = ()) -> list[dict]:
    cmd = ["helm", "template", "material-flow-control", str(CHART_DIR), "-f", str(CHART_DIR / "ci" / values)]
    for s in sets:
        cmd += ["--set", s]
    result = subprocess.run(cmd, capture_output=True, text=True)
    if result.returncode != 0:
        raise SystemExit(f"FAIL: helm template ({values}):\n{result.stderr}")
    try:
        import yaml  # type: ignore
    except ModuleNotFoundError:
        print("SKIP: PyYAML not available; cannot assert selectors", file=sys.stderr)
        raise SystemExit(0)
    return [d for d in yaml.safe_load_all(result.stdout) if d]


def matches(selector: dict, labels: dict) -> bool:
    return bool(selector) and all(labels.get(k) == v for k, v in selector.items())


def check(values: str, components: set[str], sets: tuple[str, ...] = (), services_per: dict[str, int] | None = None) -> list[str]:
    """components: the Deployments' components; services_per: Services per component when one Deployment
    backs several Services (colocated agents), else one Service per component."""
    docs = render(values, sets)
    if sets:
        values = f"{values} --set {' --set '.join(sets)}"
    deployments = [d for d in docs if d["kind"] == "Deployment"]
    services = [d for d in docs if d["kind"] == "Service"]
    failures: list[str] = []
    got = {d["spec"]["selector"]["matchLabels"].get("app.kubernetes.io/component") for d in deployments}
    if got != components or len(deployments) != len(components):
        failures.append(f"{values}: Deployments of components {sorted(got)}, want {sorted(components)}")
    want_services = sum((services_per or {}).get(c, 1) for c in components)
    if len(services) != want_services:
        failures.append(f"{values}: want {want_services} Services, got {len(services)}")
    for svc in services:
        sel = svc["spec"]["selector"]
        comp = sel.get("app.kubernetes.io/component")
        if comp not in components:
            failures.append(f"{values}: Service {svc['metadata']['name']} does not pin a component")
        hit = [d for d in deployments if matches(sel, d["spec"]["template"]["metadata"]["labels"])]
        if len(hit) != 1 or hit[0]["spec"]["selector"]["matchLabels"].get("app.kubernetes.io/component") != comp:
            failures.append(f"{values}: Service {svc['metadata']['name']} selects {[d['metadata']['name'] for d in hit]}")
    return failures


def main() -> None:
    failures = (
        check("default-values.yaml", {"api"})
        + check("all-components-values.yaml", {"api", "mcp", "a2a"})
        + check("all-components-values.yaml", {"api", "agents"}, ("agents.colocate=true",), {"agents": 2})
    )
    if failures:
        raise SystemExit("FAIL:\n  " + "\n  ".join(failures))
    print("ok: every Service selects exactly its own component's Deployment (api; api, mcp, a2a; api, agents x2 colocated)")


if __name__ == "__main__":
    main()
