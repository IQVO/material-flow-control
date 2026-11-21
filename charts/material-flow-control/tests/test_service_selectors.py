#!/usr/bin/env python3
"""Assert every Service selects exactly one Deployment, of its own component.

`app.kubernetes.io/name` + `instance` are identical on every pod of a release, so a Service must
also pin `app.kubernetes.io/component`. Rendered twice: the defaults (the api only) and
ci/all-components-values.yaml (api, mcp and a2a; ADR 0005).

Run: python3 charts/material-flow-control/tests/test_service_selectors.py
Needs: helm, PyYAML.
"""

from __future__ import annotations

import subprocess
import sys
from pathlib import Path

CHART_DIR = Path(__file__).resolve().parents[1]


def render(values: str) -> list[dict]:
    result = subprocess.run(
        ["helm", "template", "material-flow-control", str(CHART_DIR), "-f", str(CHART_DIR / "ci" / values)],
        capture_output=True, text=True,
    )
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


def check(values: str, components: set[str]) -> list[str]:
    docs = render(values)
    deployments = [d for d in docs if d["kind"] == "Deployment"]
    services = [d for d in docs if d["kind"] == "Service"]
    failures: list[str] = []
    got = {d["spec"]["selector"]["matchLabels"].get("app.kubernetes.io/component") for d in deployments}
    if got != components or len(deployments) != len(components):
        failures.append(f"{values}: Deployments of components {sorted(got)}, want {sorted(components)}")
    if len(services) != len(components):
        failures.append(f"{values}: want {len(components)} Services, got {len(services)}")
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
    failures = check("default-values.yaml", {"api"}) + check("all-components-values.yaml", {"api", "mcp", "a2a"})
    if failures:
        raise SystemExit("FAIL:\n  " + "\n  ".join(failures))
    print("ok: every Service selects exactly its own component's Deployment (api; api, mcp, a2a)")


if __name__ == "__main__":
    main()
