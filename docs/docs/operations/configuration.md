---
id: configuration
title: "Configuration"
sidebar_label: "Configuration"
description: "Every environment variable of cmd/api, cmd/mcp and cmd/a2a with its default, and the material-flow layout file."
---

# Configuration

Each binary reads its environment once at boot. A missing or invalid value is a **boot error**: the process writes
ONE JSON log line naming every bad variable and exits `1` (ruling P4-R13). Nothing else is read.

## cmd/api

Mandatory: `DATABASE_URL`, `KAFKA_BROKERS`, `ASRS_CONTROL_URL`, `AMR_FLEET_CONTROL_URL`, `MFC_LAYOUT_FILE`. Values
are trimmed of surrounding spaces; an empty value counts as unset.

| Variable | Default | Meaning |
| --- | --- | --- |
| `DATABASE_URL` | required | Postgres DSN of the service (may go through PgBouncer in transaction mode). |
| `MIGRATIONS_DATABASE_URL` | `DATABASE_URL` | Direct (session) DSN the boot migrations run against. |
| `KAFKA_BROKERS` | required | Comma-separated broker list (consumers, outbox relay, DLQ writer, `/readyz`). |
| `ASRS_CONTROL_URL` | required | Base URL of asrs-control (relationship 5), e.g. `http://asrs-control.warehouse-systems.svc.cluster.local`. |
| `AMR_FLEET_CONTROL_URL` | required | Base URL of amr-fleet-control (relationship 4), e.g. `http://amr-fleet-control.warehouse-systems.svc.cluster.local`. |
| `MFC_LAYOUT_FILE` | required | Path of the material-flow layout YAML (below). |
| `ADDR` | `:8080` | HTTP listen address. |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn` (or `warning`), `error`. |
| `MFC_ASRS_CONSUMER_GROUP` | `material-flow-control.asrs-control-events` | Consumer group of `warehouse.asrs-control.events`. |
| `MFC_AMR_CONSUMER_GROUP` | `material-flow-control.amr-fleet-control-events` | Consumer group of `warehouse.amr-fleet-control.events`. |
| `OUTBOX_INTERVAL_MS` | `250` | Relay poll interval. Positive integer. |
| `OUTBOX_BATCH` | `100` | Rows the relay publishes per pass. Positive integer. |
| `OUTBOX_MAX_ATTEMPTS` | `20` | Publish attempts before the relay stops retrying a row (it stays in the backlog gauge). Positive integer: the cap cannot be disabled (ruling P4-R14). |
| `SUBSYSTEM_TIMEOUT_MS` | `5000` | HTTP timeout of every call to asrs-control and amr-fleet-control. Positive integer. |
| `DISPATCH_POLL_MS` | `200` | Dispatcher poll interval (it is also woken after every commit that queued a command). Positive integer. |
| `TICK_INTERVAL_MS` | `1000` | How often stale legs are checked against `LEG_TIMEOUT_SECONDS`. Positive integer. |
| `LEG_RETRY_LIMIT` | `2` | New leg attempts per order after a retryable AMR `LegFailed` (decision D13). Non-negative: `0` means never retry. |
| `LEG_REQUEST_DEADLINE_SECONDS` | `300` | How long a leg request is retried under one Idempotency-Key before the transport fails `SUBSYSTEM_FAULT`. Positive integer. |
| `LEG_TIMEOUT_SECONDS` | `900` | A leg the subsystem accepted with no progress fact for this long fails the transport `TIMEOUT`. Positive integer. |
| `OTEL_SERVICE_NAME` | `material-flow-control` | OpenTelemetry `service.name`. |
| `SERVICE_VERSION` | `dev` | `service.version`. |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | empty | Collector OTLP/gRPC endpoint; empty = telemetry off. A bare `host:port` becomes `http://host:port`. |
| `ENVIRONMENT` | `local` | `deployment.environment.name` (read by the telemetry adapter). |

A positive-integer variable set to `0`, a negative number or a non-number is a boot error naming it; `LEG_RETRY_LIMIT`
refuses only negatives and non-numbers.

## cmd/mcp

| Variable | Default | Meaning |
| --- | --- | --- |
| `MCP_ADDR` | `:8090` | Listen address. |
| `DATABASE_URL` | required | The Postgres DSN `cmd/api` owns (read-only use). |
| `MIGRATIONS_DATABASE_URL` | `DATABASE_URL` | DSN of the boot migrations (this binary may boot before `cmd/api`). |
| `MFC_LAYOUT_FILE` | required | The same layout file `cmd/api` reads. |
| `LOG_LEVEL` | `info` | As for `cmd/api`. |
| `OTEL_SERVICE_NAME` | `material-flow-control-mcp` | `service.name`. |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | empty | Empty = telemetry off. |
| `SERVICE_VERSION` | `dev` | `service.version`. |
| `ENVIRONMENT` | `local` | `deployment.environment.name`. |

## cmd/a2a

| Variable | Default | Meaning |
| --- | --- | --- |
| `A2A_ADDR` | `:8093` | Listen address. |
| `A2A_AGENT_URL` | required | Absolute `http(s)` URL the Agent Card advertises, e.g. `http://material-flow-control-a2a.warehouse-systems.svc.cluster.local:8093/`. |
| `DATABASE_URL` | required | The Postgres DSN `cmd/api` owns (read-only use). |
| `MIGRATIONS_DATABASE_URL` | `DATABASE_URL` | DSN of the boot migrations. |
| `MFC_LAYOUT_FILE` | required | The same layout file `cmd/api` reads. |
| `LOG_LEVEL` | `info` | As for `cmd/api`. |
| `OTEL_SERVICE_NAME` | `material-flow-control-a2a` | `service.name`. |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | empty | Empty = telemetry off. |
| `SERVICE_VERSION` | `dev` | `service.version` and the Agent Card version. |
| `ENVIRONMENT` | `local` | `deployment.environment.name`. |

The read pools of `cmd/mcp` and `cmd/a2a` must be read-only at the session level in a deployment: their
`DATABASE_URL` carries `default_transaction_read_only=on` (ruling P4-R17).

## The material-flow layout file (decision D1)

`MFC_LAYOUT_FILE` names a YAML file, mounted from the chart's ConfigMap in a deployment:

| Key | Content |
| --- | --- |
| `siteId` | the site, e.g. `WH1` |
| `fleet` | `fleetId` (amr-fleet-control's fleet) and `legsPerRobotHour` (the per-robot rate of `capacity_per_hour`) |
| `asrs[]` | `asrsId`, the AS/RS logical `locationCode`, `retrievalsPerUnitHour`, and `ports[]` (`portId`, `kind` `INPUT`/`OUTPUT`, the port `locationCode`, the AMR `handoffPointId` where robots drop or pick) |
| `stations[]` | `stationId`, optional `locationCode`, `handoffPointId`, `bufferLimit` (at least 1) |
| `handoffs[]` | `locationCode`, `handoffPointId`, `kind` `DECANT`, `PACK`, `DOCK`, `FORWARD` or `OTHER` |
| `clarificationPoints[]` | `locationCode`, `handoffPointId`; at least one |

AS/RS and port ids must equal asrs-control's layout; handoff point ids must be amr-fleet-control map nodes. The three
files change together. `config/flow-layout.example.yaml`:

```yaml
# The example material-flow layout (ADR 0002) of the emulator's example floor: AS/RS ASRS1 (asrs-control's
# config/layout.example.yaml: ports IN1/OUT1 in aisle 1, IN2/OUT2 in aisle 2) whose port handoff points are
# amr-fleet-control's map nodes ASRS1-IN1/OUT1/IN2/OUT2; goods-to-person station ST1; the decant handoff on map node
# ST2; and PK1 (pack), FWD1 (forward area) and CLR1 (clarification), which amr-fleet-control's example map does NOT
# have yet (plan 04 Q6: plan 06 adds them). Location codes follow facility-layout's AS/RS convention (plan 05a D14).
siteId: WH1
fleet:
  fleetId: AMR1
  legsPerRobotHour: 30
asrs:
  - asrsId: ASRS1
    locationCode: WH1-AUTO-ASRS-ASRS1-00-00-S
    retrievalsPerUnitHour: 60
    ports:
      - {portId: IN1, kind: INPUT, locationCode: WH1-AUTO-ASRS-ASRS1-01-00-IN1, handoffPointId: ASRS1-IN1}
      - {portId: OUT1, kind: OUTPUT, locationCode: WH1-AUTO-ASRS-ASRS1-01-00-OUT1, handoffPointId: ASRS1-OUT1}
      - {portId: IN2, kind: INPUT, locationCode: WH1-AUTO-ASRS-ASRS1-02-00-IN2, handoffPointId: ASRS1-IN2}
      - {portId: OUT2, kind: OUTPUT, locationCode: WH1-AUTO-ASRS-ASRS1-02-00-OUT2, handoffPointId: ASRS1-OUT2}
stations:
  - {stationId: ST1, locationCode: WH1-GTP-ST1, handoffPointId: ST1, bufferLimit: 2}
handoffs:
  - {locationCode: WH1-RCV-DEC1, handoffPointId: ST2, kind: DECANT}
  - {locationCode: WH1-PCK-PK1, handoffPointId: PK1, kind: PACK}
  - {locationCode: WH1-FWD-FWD1, handoffPointId: FWD1, kind: FORWARD}
clarificationPoints:
  - {locationCode: WH1-CLR-CLR1, handoffPointId: CLR1}
```

The example codes (`WH1-GTP-ST1`, `WH1-RCV-DEC1`, ...) are example data; a deployed layout uses the site's real
facility-layout location codes.

**Station buffer limits are configured here** ([ADR 0001](/docs/adr/0001-material-flow-control-bounded-context),
decision 4): `stations[].bufferLimit` is this context's configuration, not a facility-layout attribute. Changing a
buffer is a ConfigMap change and a restart.
