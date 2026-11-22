---
id: runbook
title: "Runbook"
sidebar_label: "Runbook"
description: "Boot and shutdown order, the probes, dead-letter topics and their replay, stuck subsystem commands, clarification, leg timeouts and table retention."
---

# Runbook

## Boot

`cmd/api` boots in this order (`cmd/api/main.go`, `wire.go`):

1. The JSON logger at the default level, then the environment; a bad environment is one JSON error line and exit `1`.
2. OpenTelemetry (nothing installed when `OTEL_EXPORTER_OTLP_ENDPOINT` is empty).
3. `net.Listen` on `ADDR`: a busy address fails the boot before anything else is wired.
4. The layout file, the embedded migrations (on `MIGRATIONS_DATABASE_URL`), the Postgres pool, the metrics and the
   use cases.
5. The loops (dispatcher, tick, hourly purge, outbox relay), the two consumers, then the HTTP server.

`cmd/mcp` and `cmd/a2a` load the environment, start telemetry, listen, then load the layout, run the migrations and
open the pool.

## Shutdown

On `SIGTERM` or `SIGINT` (or when the HTTP server fails), `cmd/api`:

1. flips `/readyz` to `503` (`not_ready`),
2. cancels the run context: the loops and the consumers stop,
3. closes each consumer as soon as its own run returned, concurrently, while the HTTP server drains (10 s bound),
4. waits for the loops, then closes the Kafka sink and the Postgres pool.

A command queued during the drain stays `PENDING` in `subsystem_commands` and is sent after the restart.

## Probes

| Probe | Answers |
| --- | --- |
| `GET /healthz` | `200 {"status":"ok"}` while the process runs. Liveness. |
| `GET /readyz` | Readiness. `200 {"status":"ready"}`; `503 {"status":"not_ready"}` while draining; `503` problem `dependency-unavailable` naming each failing check: `postgres` (ping), `kafka` (broker dial), `subsystem-facts` from boot until both consumers caught up with the backlog sampled at boot (ruling P4-R20: no POST is decided and the leg-timeout tick does not run meanwhile). The checks run in turn under ONE 1 s deadline and the result is cached 2 s; `subsystem-facts` reads the catch-up gate's last result (no broker call, answers at once), so a slow broker never delays it. |

`cmd/mcp` and `cmd/a2a` serve `GET /healthz` only.

## Dead-letter topics

| Source topic | DLQ |
| --- | --- |
| `warehouse.asrs-control.events` | `warehouse.asrs-control.events.dlq` |
| `warehouse.amr-fleet-control.events` | `warehouse.amr-fleet-control.events.dlq` |

A message lands there at once when it is not a CloudEvent or carries a consumed type with a malformed payload or a
missing required field, and after 5 failed attempts (backoff 200 ms to 5 s) when its handler fails transiently. The
original key, value and headers are kept, plus `x-dlq-source-topic`, `x-dlq-source-partition`, `x-dlq-source-offset`,
`x-dlq-error` and `x-dlq-failed-at`. The consumer then commits past it, so the partition never blocks.

**Replaying one message:**

1. Read it with its headers: `kcat -b $KAFKA_BROKERS -C -t warehouse.asrs-control.events.dlq -o -1 -e -f '%k %h\n%s\n'`.
2. Fix the cause `x-dlq-error` names (a producer bug, a layout that does not name the AS/RS or the handoff point).
3. Check that its CloudEvents id is NOT in `processed_events`
   (`SELECT 1 FROM processed_events WHERE event_id = '<id>'`): a dead-lettered message never committed its inbox
   claim, so a replay applies it once.
4. Produce the original value under its original key back to the source topic
   (`kcat -b $KAFKA_BROKERS -P -t warehouse.asrs-control.events -K '\t'`). Every fleet consumer of that topic dedupes on
   the CloudEvents id, so a consumer that already applied it skips it.

## A stuck subsystem command

```sql
SELECT seq, kind, transport_order_id, leg_ref, attempts, next_attempt_at, deadline, last_error
FROM subsystem_commands WHERE status = 'PENDING' ORDER BY next_attempt_at, seq;
```

A `PENDING` row with a growing `attempts` and a `last_error` is being retried (a RETRYABLE refusal, a 5xx, a timeout or
a refused connection) under the same Idempotency-Key until its `deadline`; then it becomes `FAILED`. A FINAL refusal
(`bad-request`, `invalid-parameter`, `asrs-not-found`, `unknown-port`, `wrong-port-kind`, `idempotency-key-reused`)
makes it `FAILED` at once. What follows depends on the row's `kind`:

| `kind` | Past its `deadline` | FINAL refusal |
| --- | --- | --- |
| `REQUEST_LEG` | The transport fails `SUBSYSTEM_FAULT` with the last code (or `subsystem-unreachable`) in `subsystem_reason`. | The transport fails `SUBSYSTEM_FAULT` with the subsystem's code in `subsystem_reason`, and `a subsystem refused a leg for good: the layout and the subsystem disagree` is logged at ERROR: the layout file and the subsystem's layout or map disagree. |
| `CANCEL_LEG` | The cancellation is recorded as refused (`CancelRefused`): the transport keeps running to its outcome. | The same (a not-cancellable code counts as FINAL here): the transport keeps running. |
| `CONFIRM_PICKUP` | `a pickup confirmation expired: the AS/RS port may stay blocked` is logged at ERROR; the order is untouched. | `asrs-control refused a pickup confirmation` is logged at ERROR; the order is untouched. |

Fix the configuration; never edit the row.

## A container in clarification

`GET /container-tracks/{containerId}` shows the position and `clarification` (`reason`, `detected_at_location`,
`scanned_container_id`, `since`). A held container is never dispatched, and a new request for it is rejected
`CONTAINER_BUSY`. After an operator checked the tote:

```bash
curl -X POST "$MFC/container-tracks/TOTE-1/clarification-resolution" -H 'Idempotency-Key: resolve-TOTE-1-1' \
  -H 'Content-Type: application/json' -d '{"location":"WH1-CLR-CLR1"}'
```

`location` (optional) is where the container now is; the default is where the track is. `409 not-in-clarification`
means it was already resolved; `422 unknown-location` names a place outside the layout.

## LEG_TIMEOUT failures

`TransportFailed` with `reason` `TIMEOUT` means a leg the subsystem accepted showed no progress fact for
`LEG_TIMEOUT_SECONDS` (default 900). The tick also queues a best-effort `CANCEL_LEG`. A later fact for that leg still
moves the container's track (the track follows physical facts) but never reopens the order. A leg that timed out
with the container on a robot holds the container `CONTAINER_ON_FAILED_ROBOT` (ruling P4-R9). Check the subsystem
first: its consumer lag and its own logs for the leg reference `<orderId>.<seq>.<attempt>`.

## Table retention

The hourly purge of `cmd/api` keeps the operational tables bounded
([ADR 0008](/docs/adr/0008-retention-of-the-operational-tables)). It deletes only terminal rows older than their
window, in batches of `PURGE_BATCH` rows (one transaction each, repeated until the table is done):

| Table | Purged | Window |
| --- | --- | --- |
| `transport_orders` (+ `transport_legs`) | `COMPLETED`/`FAILED` orders no pending command, container track or station queue still references | `ORDER_RETENTION_HOURS` (720) |
| `container_moves` | moves beyond each container's latest 20, unless a live order moves the container | `CONTAINER_MOVE_RETENTION_HOURS` (720) |
| `subsystem_commands` | `DONE`/`FAILED` commands of ended orders | `COMMAND_RETENTION_HOURS` (720) |
| `outbox_events` | published rows | `OUTBOX_RETENTION_HOURS` (168) |
| `idempotency_keys`, `processed_events` | every row | 24 hours, 30 days (fixed) |

`material_flow_control.retention.purged{table}` counts the deleted rows; `purge failed` (WARN, `table`) names a pass
that failed (the next hour retries it). `transport_requests` is never purged (ruling P4-R22): after the window the
order is gone but its record stays, so `GET /transport-requests/{id}`, a retried POST and a cancellation answer
`410 transport-request-expired`, and the id is never accepted again. To find what the next pass will delete:

```sql
SELECT count(*) FROM transport_orders WHERE status IN ('COMPLETED', 'FAILED') AND updated_at < now() - interval '720 hours';
SELECT count(*) FROM outbox_events WHERE published_at < now() - interval '168 hours';
```

Migration `0002_retention_indexes` runs at the first boot of the release that brings the retention (every binary
runs the embedded migrations at boot). It changes four order id columns to `COLLATE "C"` and builds eight indexes in
ONE transaction ([ADR 0008](/docs/adr/0008-retention-of-the-operational-tables), decision 5): until it commits,
`transport_legs`, `subsystem_commands`, `container_tracks` and `transport_requests` are locked even for reads, and
`transport_orders`, `container_moves` and `outbox_events` for writes. At 300k orders and 1M moves it took about 1 s;
it grows with the tables, so check them first (`SELECT relname, n_live_tup FROM pg_stat_user_tables ORDER BY 2
DESC`). Its `ALTER TABLE` also waits for every open transaction on those tables, and new queries queue behind it:
look for long-open sessions first (`SELECT pid, state, xact_start FROM pg_stat_activity WHERE xact_start < now() -
interval '1 minute'`). On a database with many millions of moves, apply it before the rollout, in a quiet window,
with the golang-migrate CLI (`migrate -path migrations -database "$MIGRATIONS_DATABASE_URL" up`), so the boot finds
nothing to do inside its startup-probe budget (60 s).

An attempt-capped outbox row is never purged: re-arm it (`UPDATE outbox_events SET attempts = 0 WHERE id = ...`) once
the cause is fixed. An order a station still holds as occupant, or a container still in clarification, keeps its
order until the hold or the slot is released.
