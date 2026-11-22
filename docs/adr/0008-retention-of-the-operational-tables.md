# ADR 0008: Retention of the operational tables

## Status

Accepted

## Context

Until issue #3, `cmd/api` purged only `idempotency_keys` (the 24-hour window) and `processed_events` (30 days).
`container_moves`, ended `transport_orders` with their `transport_legs` and `transport_requests` records,
`DONE`/`FAILED` `subsystem_commands` and published `outbox_events` grew without bound. Plan 04 Task 13 deferred the
decision and Task 26's review found it missing (finding 4). Retention must keep what the read side and audit still
need: the relationship-1 view of a request (`GET /transport-requests/{id}`) and the MCP/A2A `get_transport` read the
request record and its order. A container's track shows its last `HistoryLimit` (20) moves. The dispatcher reads
`PENDING` commands, the relay reads unpublished outbox rows, and station queues and container tracks name the
orders they hold.

## Decision

1. The hourly purge loop of `cmd/api` runs five more policies, in this order, each one a batched `DELETE` (at most
   `PURGE_BATCH` rows per transaction, default 1000, repeated until a batch comes back short). Each batch is its own
   transaction with the adapter's `SET LOCAL` statement and lock timeouts (PgBouncer transaction pooling).

   | Policy (`table`) | Deletes | Window (env, default) | Never deletes |
   | --- | --- | --- | --- |
   | `transport_orders` | `COMPLETED`/`FAILED` orders last written (`updated_at`) before the cutoff, with their legs (cascade) and their ACCEPTED request record, in one statement | `ORDER_RETENTION_HOURS`, 720 h (30 days), at least 24 h | an order with a `PENDING` command, an order a container track holds active or in clarification, an order a station queue holds as occupant or waiting entry, any `IN_PROGRESS` order |
   | `transport_requests` | REJECTED request records recorded before the cutoff | `ORDER_RETENTION_HOURS` | ACCEPTED records (they go with their order) |
   | `container_moves` | moves made before the cutoff | `CONTAINER_MOVE_RETENTION_HOURS`, 720 h | a container's latest `HistoryLimit` moves; any move of a container an `IN_PROGRESS` order is moving |
   | `subsystem_commands` | `DONE`/`FAILED` commands created before the cutoff | `COMMAND_RETENTION_HOURS`, 720 h | `PENDING` commands; commands of an `IN_PROGRESS` order |
   | `outbox_events` | rows published before the cutoff | `OUTBOX_RETENTION_HOURS`, 168 h (7 days) | unpublished rows, attempt-capped ones included |

2. The order batch takes the flow lock first (decision D19), so it never interleaves with a unit of work that
   reads an order or its request record and then expects it to exist (a `RequestTransport` replay, a late fact).
   The other policies delete rows no writer changes again, so they run without the lock.
3. Why these windows: 30 days for an order and its request record keeps a transport's view, its legs and its
   command trail for an operator's or an agent's investigation of last month. It is the same horizon as the inbox
   (`processed_events`). The order window is never under 24 hours: a WES retries a `transport_request_id` within
   the 24-hour idempotency window, and a retry after its record was purged would be accepted as a new request and
   move the container a second time. A shorter value is a boot error. Moves keep the same 30 days, plus the
   latest `HistoryLimit` per container, so a container at rest for months still shows where it came from. Published
   outbox rows are on the Kafka topic already; 7 days covers a relay or broker incident investigation (the fleet
   value: inventory-storage's `OUTBOX_RETENTION`).
4. Every pass adds the rows it deleted to `material_flow_control.retention.purged` (`{row}`, attribute `table`;
   the idempotency and inbox purges included). A pass that deleted nothing adds 0, and a failed pass adds nothing
   and logs `purge failed`. The instrument name and its attribute are a dashboard contract, as in ADR 0007.
5. Migration `0002_retention_indexes` adds the indexes the policies and their guards probe, so no policy scans a
   table per candidate row.

## Consequences

- Responsibility 6 (a retried `transport_request_id` returns its original outcome) holds for `ORDER_RETENTION_HOURS`.
  After that the record is gone: a read is `404 transport-request-not-found` and a new POST under the id is a new
  request. No request record ever names a purged order, so the view never fails on a missing order.
- A late subsystem fact for a purged order's leg finds no leg reference and is ignored as foreign, exactly as a fact
  for a leg this context never owned.
- A container in clarification, or one occupying a station after its order ended, keeps that order until it is
  released. The purge never breaks the hold or the slot.
- The flow lock is held for one order batch at a time: lower `PURGE_BATCH` if a batch ever shows in the
  writers' lock waits.
