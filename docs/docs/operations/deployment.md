---
id: deployment
title: "Deployment"
sidebar_label: "Deployment"
description: "How material-flow-control is deployed: one api replica and why, the layout ConfigMap, the read-only agent deployments and the Kong routes."
---

# Deployment

material-flow-control ships one image that holds the three binaries (`/app/api`, `/app/mcp`, `/app/a2a`; the
Dockerfile builds every `cmd/*`). The Helm chart `charts/material-flow-control/` deploys it in the
`warehouse-systems` namespace; ArgoCD reads the chart from `develop`.

:::note
The chart is added by the packaging step of the build plan and is not in this repository yet. This page states the
deployment contract the code requires and the chart implements.
:::

## One api replica, and why

The api Deployment runs **exactly one replica** with `strategy: Recreate`, and the chart refuses any other
`replicaCount`. The dispatcher (`subsystem_commands` is read without `SKIP LOCKED`) and the two fact consumers assume
one process (decision D19). The flow lock keeps a second process correct, but a second dispatcher would race the first
on the same due commands. `cmd/mcp` and
`cmd/a2a` are read-only and may scale.

`terminationGracePeriodSeconds` must cover the shutdown sequence of the [Runbook](/docs/operations/runbook#shutdown)
(10 s HTTP drain plus the consumer close): at least 30 s.

## Configuration

| Source | Holds |
| --- | --- |
| ConfigMap | every non-secret `cmd/api` variable of [Configuration](/docs/operations/configuration), plus `flow-layout.yaml`, mounted read-only at `/etc/material-flow-control/flow-layout.yaml` (`MFC_LAYOUT_FILE`) |
| Secret | `DATABASE_URL` and `MIGRATIONS_DATABASE_URL` |

The mcp and a2a Deployments mount the same layout item and set `MFC_LAYOUT_FILE`, `DATABASE_URL` (from the Secret,
with `default_transaction_read_only=on`), `MIGRATIONS_DATABASE_URL`, `OTEL_SERVICE_NAME` and `MCP_ADDR` / `A2A_ADDR`
(plus `A2A_AGENT_URL`); never `KAFKA_BROKERS` nor a subsystem URL.

Probes: liveness `GET /healthz`, readiness `GET /readyz` on `:8080`.

## Routes

| Route (Kong) | Backend | Rewrite |
| --- | --- | --- |
| `/api/material-flow-control` | the api Service (:80 to :8080) | to `/` |
| `/api/mcp/material-flow-control` | the mcp Service (:8090) | to `/mcp` |
| `/api/a2a/material-flow-control` | the a2a Service (:8093) | to `/` |

The routes and the KongPlugins (`cors`, `prometheus`, `opentelemetry`) are default-off values. No auth plugin.

## Changing the layout

The layout lives in the ConfigMap and is read once at boot: a buffer limit or a new handoff point is a ConfigMap
change plus a restart, made together with the matching change of asrs-control's layout and amr-fleet-control's map.
