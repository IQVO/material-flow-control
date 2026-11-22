---
id: observability
title: "Observability"
sidebar_label: "Observability"
description: "OpenTelemetry Tier 1 in all three binaries, the transports-ended counter, the outbox gauges, the log fields and the Kong plugins."
---

# Observability

material-flow-control follows the fleet OpenTelemetry standard
([ADR 0007](/docs/adr/0007-observability-and-the-read-only-agentic-surfaces)): traces and metrics over OTLP/gRPC to the
Collector and JSON logs on stdout carrying the trace id, in `cmd/api`, `cmd/mcp` and `cmd/a2a`. With
`OTEL_EXPORTER_OTLP_ENDPOINT` empty, telemetry is **off**: nothing is installed and nothing is dialled. Export never
blocks: an unreachable Collector costs telemetry, not availability. `service.name` defaults to
`material-flow-control`, `material-flow-control-mcp` and `material-flow-control-a2a`.

## Tier 1: traces

| Span | Kind | Where |
| --- | --- | --- |
| the chi route pattern, e.g. `/transport-requests/{transportRequestId}/cancellation` | server | every REST, MCP and A2A HTTP request (otelchi) |
| `warehouse.asrs-control.events process`, `warehouse.amr-fleet-control.events process` | consumer | every consumed message; continues the producer's `traceparent` header |
| HTTP client spans | client | every call to asrs-control and amr-fleet-control (otelhttp transport, which injects `traceparent`) |
| `warehouse.material-flow-control.events publish`, `warehouse.material-flow-control.analytics publish` | producer | every outbox row the relay publishes (`messaging.system=kafka`, `cloudevents.event_type`); continues the trace of the unit of work that wrote the row |
| `mcp.tool <name>` | internal | every MCP tool call (`gen_ai.operation.name=execute_tool`, `gen_ai.tool.name`, `warehouse.authority=L1`) |
| `a2a.skill <id>`, `a2a.skill unknown` | internal | every A2A skill call (same attributes; an unknown skill id is one low-cardinality span) |

W3C trace context propagates on REST in and out and on Kafka in and out.

## Tier 1: metrics

| Series | Kind | Labels | Use |
| --- | --- | --- | --- |
| `http.server.request.duration` | histogram | route, method, status | REST, MCP and A2A RED |
| Go runtime (`go.*`, `process.runtime.*`) | gauges | none | goroutines, GC, heap |

## Tier 2: metrics of cmd/api

| Series | Kind | Labels | Use |
| --- | --- | --- | --- |
| `material_flow_control.transports.ended` | counter (`{transport}`) | `purpose`, `outcome` (`completed`, `failed`), `reason` | accepted transports ended (ADR 0007); recorded after commit, so a replay or a redelivery never counts twice; a rising `failed` with `SUBSYSTEM_FAULT` or `TIMEOUT` points at a subsystem |
| `material_flow_control.retention.purged` | counter (`{row}`) | `table` | rows the hourly purge deleted ([ADR 0008](/docs/adr/0008-retention-of-the-operational-tables)): `transport_orders`, `transport_requests`, `container_moves`, `subsystem_commands`, `outbox_events`, `idempotency_keys`, `processed_events`; every successful pass adds its count (0 included), a failed pass adds nothing |
| `material_flow_control.outbox.backlog` | gauge (`{row}`) | none | unpublished outbox rows (rows the relay stopped retrying at `OUTBOX_MAX_ATTEMPTS` included) |
| `material_flow_control.outbox.lag` | gauge (s) | none | age of the oldest unpublished row; 0 when drained. A growing lag means Kafka is not taking the relay's writes |

The two outbox gauges are registered by `cmd/api` (ruling P4-R15). In Prometheus the dots become underscores and
counters get `_total` (for example `material_flow_control_transports_ended_total`). Dashboards and alert rules live in
warehouse-infra.

## Logs

JSON on stdout through `log/slog`, level from `LOG_LEVEL`. Records written under a span carry `trace_id` and `span_id`.
Every HTTP request writes one access line with `method`, `path`, `status`, `duration_ms` and `request_id`. Lines worth
alerting on:

| Message | Level | Meaning |
| --- | --- | --- |
| `a subsystem refused a leg for good: the layout and the subsystem disagree` | ERROR | a FINAL refusal of a leg request; the transport failed `SUBSYSTEM_FAULT` |
| `asrs-control refused a pickup confirmation` | ERROR | a `CONFIRM_PICKUP` was refused (`code` field) |
| `a pickup confirmation expired: the AS/RS port may stay blocked` | ERROR | a `CONFIRM_PICKUP` reached its deadline |
| `dispatch failed`, `tick failed` | WARN | a dispatcher or tick pass failed; the next pass retries |
| `consumer stopped` | ERROR | a consumer's run returned with an error |
| `idempotency: re-read before an unstored refusal failed` | WARN | the key re-read before an unstored refusal failed ([Replay classes](/docs/development/replay-classes)); the request's own refusal went out |
| `purge failed` | WARN | the hourly purge of one table failed (`table` field); the next pass retries |
| `purged` | INFO | a purge pass deleted rows (`table`, `rows`) |
| `outbox relay: row reached the attempt cap and is excluded from future claims; re-arm it with attempts = 0 after fixing the cause` | ERROR | an outbox row failed `OUTBOX_MAX_ATTEMPTS` times |
| `outbox gauges unavailable` | WARN | the gauges could not be registered; the boot continues |
| `telemetry disabled: OTEL_EXPORTER_OTLP_ENDPOINT is not set` | INFO | boot without a Collector |

## Kong plugins

Behind Kong, the chart attaches the KongPlugins `cors`, `prometheus` and `opentelemetry` to every route
(`/api/material-flow-control`, `/api/mcp/material-flow-control`, `/api/a2a/material-flow-control`) through the
`konghq.com/plugins` annotation. No auth plugin, by fleet decision: the services have no authentication of any kind.
See [Deployment](/docs/operations/deployment).

## Other signals

| Signal | Where |
| --- | --- |
| Readiness and the failing dependency | `GET /readyz` |
| Capacity of the fleet, the AS/RS and the stations | `GET /subsystems`, `SubsystemCapacityChanged` |
| Pending and failed subsystem commands | `subsystem_commands` ([Runbook](/docs/operations/runbook#a-stuck-subsystem-command)) |
| Unpublished events | `outbox_events` with `published_at IS NULL` |
