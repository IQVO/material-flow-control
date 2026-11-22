---
id: deployment
title: "Deployment"
sidebar_label: "Deployment"
description: "How material-flow-control is deployed: one api replica and why, the layout ConfigMap, the read-only agent deployments (optionally one agents pod) and the Kong routes."
---

# Deployment

material-flow-control ships one image that holds the three binaries (`/app/api`, `/app/mcp`, `/app/a2a`; the
Dockerfile builds every `cmd/*`, runs as user 1000, exposes 8080, 8090 and 8093, and bakes no layout in: the chart
mounts it). The image's entrypoint is `/app/api`; the chart starts the agents with `command: ["/app/mcp"]` and
`["/app/a2a"]`. The Helm chart `charts/material-flow-control/` deploys it in the `warehouse-systems` namespace;
ArgoCD reads the chart from `develop`. The chart refuses to render without `database.url` (or
`database.existingSecret`), `kafka.brokers` and both subsystem URLs.

## One api replica, and why

The api Deployment runs **exactly one replica** with `strategy: Recreate`, and the chart refuses any other
`replicaCount`. The dispatcher (`subsystem_commands` is read without `SKIP LOCKED`) and the two fact consumers assume
one process (decision D19). The flow lock keeps a second process correct, but a second dispatcher would race the first
on the same due commands. `cmd/mcp` and
`cmd/a2a` are read-only and may scale.

`terminationGracePeriodSeconds` must cover the shutdown sequence of the [Runbook](/docs/operations/runbook#shutdown):
the 10 s HTTP drain overlapping the consumers' close and one relay pass (Kafka writes with 10 s timeouts, 2 attempts),
about 30 s. The chart's default is 45 s and it refuses less than 30.

## Configuration

| Source | Holds |
| --- | --- |
| ConfigMap | every non-secret `cmd/api` variable of [Configuration](/docs/operations/configuration), plus `flow-layout.yaml`, mounted read-only at `/etc/material-flow-control/flow-layout.yaml` (`MFC_LAYOUT_FILE`) |
| Secret | `DATABASE_URL` and `MIGRATIONS_DATABASE_URL` |

`ENVIRONMENT` (`deployment.environment.name` on every span and metric; the binaries default to `local`) comes from the
value `environment`, default `production`, for the api, mcp and a2a alike.

The Secret the chart renders from `database.url` holds `DATABASE_URL` only; with `database.existingSecret` the
Deployments read `DATABASE_URL` and, when present, `MIGRATIONS_DATABASE_URL` (the owner DSN the migrations run with;
`optional`, falling back to `DATABASE_URL`) from that Secret.

The mcp and a2a Deployments mount the same layout item and set exactly what their binaries read: `MFC_LAYOUT_FILE`,
`DATABASE_URL` and `MIGRATIONS_DATABASE_URL` (from the Secret), `LOG_LEVEL`, `SERVICE_VERSION`, `ENVIRONMENT`,
`OTEL_SERVICE_NAME` (`material-flow-control-mcp` / `-a2a`), `OTEL_EXPORTER_OTLP_ENDPOINT` and `MCP_ADDR` / `A2A_ADDR` (plus
`A2A_AGENT_URL`, by default `http://<release>-a2a.<namespace>.svc.cluster.local:8093/`); never `KAFKA_BROKERS` nor a
subsystem URL. Their database access is read-only in code (ruling P4-R17): every transaction they begin is
`BEGIN READ ONLY`, so a write fails with SQLSTATE 25006 even through PgBouncer's transaction pooling, which may refuse
a `default_transaction_read_only` startup parameter. The DSN needs no read-only option.

Probes: startup `GET /healthz` (2 s period, 30 failures: the boot runs the migrations and dials Kafka first),
liveness `GET /healthz`, readiness `GET /readyz` on `:8080`. The agents probe `GET /healthz` on their own port.

## Resources

| Container | Requests | Limit |
| --- | --- | --- |
| api | `cpu: 50m`, `memory: 64Mi` | `memory: 256Mi` |
| mcp | `cpu: 25m`, `memory: 32Mi` | `memory: 128Mi` |
| a2a | `cpu: 25m`, `memory: 32Mi` | `memory: 128Mi` |

The cluster schedules against requests, so they are kept small; the limits leave room for a burst.

## One agents pod (agents.colocate)

`agents.colocate` (default `false`; capacity step 3, warehouse-infra ADR 0012) with both `mcp.enabled` and
`a2a.enabled` renders ONE `<release>-agents` Deployment with the two containers `mcp` (:8090) and `a2a` (:8093) instead
of the two standalone Deployments. The containers are the standalone ones verbatim (same binaries, env, probes,
resources and OTel service names); only the port names change, `http` to `mcp` / `a2a`. The `<release>-mcp` and
`<release>-a2a` Services keep their names and ports and switch their selector to `component: agents` and their
`targetPort` to `mcp` / `a2a`; the routes, the KongPlugins and `A2A_AGENT_URL` are unchanged. The agents pod runs
`mcp.replicaCount` replicas; `agents.strategy` overrides its rollout strategy (`{}` = RollingUpdate,
`{type: Recreate}` only when no surge slot is free). With only one of mcp/a2a on, the chart renders the standalone
Deployment. The render fails if the mcp and a2a pod specs ever diverge.

Readiness is then pod-wide: if either container is unready, both the MCP and the A2A endpoint leave the Service
endpoints; liveness stays per container. The api pod is never part of it.

Cutover is by a new object name: `<release>-agents` carries `argocd.argoproj.io/sync-wave: "-1"` (ArgoCD creates it
and waits for it to be Healthy before the Services re-select it in wave 0), and the standalone mcp and a2a Deployments
carry `argocd.argoproj.io/sync-options: PruneLast=true` (pruned last). With the flag off, those annotations are the
only render difference. warehouse-infra turns it on per context in `helm-values/material-flow-control.yaml`
(`agents: {colocate: true}`); rollback is reverting that line.

## Routes

| Route (Kong) | Backend | Rewrite |
| --- | --- | --- |
| `/api/material-flow-control` | the api Service (:80 to :8080) | to `/` |
| `/api/mcp/material-flow-control` | the mcp Service (:8090) | to `/mcp` |
| `/api/a2a/material-flow-control` | the a2a Service (:8093) | to `/` |

The routes and the KongPlugins (`cors`, `prometheus`, `opentelemetry`) are default-off values
(`gatewayApi.enabled`, `mcp.route.enabled`, `a2a.route.enabled`, `kongPlugins.*.enabled`); every route names the
enabled plugins in `konghq.com/plugins`. No auth plugin.

The chart reads the platform keys warehouse-infra's `helm-values/_platform.yaml` supplies, like the sibling charts:
`kongPlugins.cors.origin` (the exact web origin; a `kongPlugins.cors.origins` list is accepted too),
`kongPlugins.opentelemetry.tracesEndpoint`, `gatewayApi.parentRefs` and `mcp.route.parentRefs` /
`a2a.route.parentRefs` (each falls back to `gatewayApi.parentRefs` when empty). It refuses to render an enabled
`cors` without an origin or with `"*"`, and an enabled `opentelemetry` without a traces endpoint.

## Chart tests

`charts/material-flow-control/tests/` (run by CI's `chart-tests` job): `render_test.sh` (the api env is exactly what
`cmd/api` reads, the mcp and a2a env exactly what their binaries read with both database URLs from the Secret, the
mounted layout is `config/flow-layout.example.yaml`, one replica with `Recreate`, the grace period, the guards and
the default-off routes), `test_service_selectors.py` (every Service selects exactly its own component's Deployment,
also with `agents.colocate=true`, where both agent Services select the one `agents` Deployment),
`test_kong_config.py` (the three routes, their rewrites and plugins, no auth plugin, CORS headers, the origin and
traces endpoint from the platform keys) and `test_agents_colocation.py` (the fleet's canonical agents.colocate test
from warehouse-harness-template: flag off renders the standalone Deployments with `PruneLast`, flag on one `-agents`
Deployment whose containers equal the standalone ones except the port name, every named `targetPort` resolving to
exactly one containerPort, routes and every other object unchanged, and the fallback without a2a). `render_test.sh` also renders `tests/fixtures/platform-values.yaml` (a copy
of warehouse-infra's `_platform.yaml`) under an asrs-control-shaped application file, as ArgoCD layers them.

## Changing the layout

The layout lives in the ConfigMap (`layout` in the values; empty = the chart's `files/flow-layout.yaml`, a copy of
the example layout) and is read once at boot: a buffer limit or a new handoff point is a ConfigMap change plus a
restart, made together with the matching change of asrs-control's layout and amr-fleet-control's map. The example's
place codes are examples; a deployed layout uses the site's real facility-layout location codes.
