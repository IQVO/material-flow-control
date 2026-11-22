# ADR 0008: Retention of the operational tables

## Status

Accepted

## Context

Until issue #3, `cmd/api` purged only `idempotency_keys` (the 24-hour window) and `processed_events` (30 days).
`container_moves`, ended `transport_orders` with their `transport_legs`, `DONE`/`FAILED` `subsystem_commands` and
published `outbox_events` grew without bound. Plan 04 Task 13 deferred the
decision and Task 26's review found it missing (finding 4). Retention must keep what the read side and audit still
need: the relationship-1 view of a request (`GET /transport-requests/{id}`) and the MCP/A2A `get_transport` read the
request record and its order. A container's track shows its last `HistoryLimit` (20) moves. The dispatcher reads
`PENDING` commands, the relay reads unpublished outbox rows, and station queues and container tracks name the
orders they hold.

## Decision

1. The hourly purge loop of `cmd/api` runs four more policies, in this order, each one a batched `DELETE` (at most
   `PURGE_BATCH` rows per transaction, default 1000, repeated until a batch comes back short). Each batch is its own
   transaction with the adapter's `SET LOCAL` statement and lock timeouts (PgBouncer transaction pooling).

   | Policy (`table`) | Deletes | Window (env, default) | Never deletes |
   | --- | --- | --- | --- |
   | `transport_orders` | `COMPLETED`/`FAILED` orders last written (`updated_at`) before the cutoff, with their legs, in one statement | `ORDER_RETENTION_HOURS`, 720 h (30 days), at least 24 h | an order with a `PENDING` command, an order a container track holds active or in clarification, an order a station queue holds as occupant or waiting entry, any `IN_PROGRESS` order; its request record (decision 6) |
   | `container_moves` | moves made before the cutoff | `CONTAINER_MOVE_RETENTION_HOURS`, 720 h | a container's latest `HistoryLimit` moves; any move of a container an `IN_PROGRESS` order is moving |
   | `subsystem_commands` | `DONE`/`FAILED` commands created before the cutoff | `COMMAND_RETENTION_HOURS`, 720 h | `PENDING` commands; commands of an `IN_PROGRESS` order |
   | `outbox_events` | rows published before the cutoff | `OUTBOX_RETENTION_HOURS`, 168 h (7 days) | unpublished rows, attempt-capped ones included |

   Every window is at most 87600 h (ten years): `time.Duration` overflows past 2562047 h, and an overflowed window
   would put the cutoff in the future. A larger value is a boot error. The moves policy reads its candidates in
   `(moved_at, id)` order after a cursor, so one pass reads every row once, the rows its guards keep included.

2. The order batch takes the flow lock first (decision D19), before it touches an order row, so it never
   interleaves with a unit of work that reads an order and then expects it to exist (a cancellation, a late fact).
   The other policies delete rows no writer changes again, so they run without the lock.
3. Why these windows: 30 days for an order keeps a transport's view, its legs and its command trail for an
   operator's or an agent's investigation of last month. It is the same horizon as the inbox (`processed_events`).
   The order window is never under 24 hours, the idempotency window a WES retries in, so a retry inside it still
   answers the transport's current view. A shorter value is a boot error. Moves keep the same 30 days, plus the
   latest `HistoryLimit` per container, so a container at rest for months still shows where it came from. Published
   outbox rows are on the Kafka topic already; 7 days covers a relay or broker incident investigation (the fleet
   value: inventory-storage's `OUTBOX_RETENTION`).
4. Every pass adds the rows it deleted to `material_flow_control.retention.purged` (`{row}`, attribute `table`;
   the idempotency and inbox purges included; the order statement's legs under `transport_legs`). A pass that deleted nothing adds 0, and a failed pass adds nothing
   and logs `purge failed`. The instrument name and its attribute are a dashboard contract, as in ADR 0007.
5. Migration `0002_retention_indexes` adds the indexes the policies and their guards probe, so no policy scans a
   table per candidate row: `transport_legs (transport_order_id)` for the order's legs and its FK cascade, and an
   index per guard. Every column holding a transport order id moves to `COLLATE "C"`, the collation of
   `transport_orders`' key: a comparison with the key runs under `"C"`, which a default-collation index cannot
   serve. A schema test fails any foreign key into a purged table without an index led by its columns.
6. Request records are never deleted (ruling P4-R22). A `transport_requests` row is the tombstone of its
   `transport_request_id`: the order policy deletes the order and its legs and keeps the ACCEPTED record, and no
   policy deletes a REJECTED record. Once the order is purged, a read of the id (`GET /transport-requests/{id}`,
   MCP `get_transport`, A2A `transport_status`), a retry of the same request and a cancellation answer
   `410 transport-request-expired`, replay class FINAL: the id never becomes new again, and a retry never
   re-executes. The same id with another request is still `409 transport-request-conflict`, because the record keeps
   the request. A read that races the purge (record read, then the order gone) answers the same 410. The cost is one
   row per request id for the life of the database (about 0.3 KB each); rejected and accepted ids alike.

## Consequences

- Responsibility 6 (a retried `transport_request_id` returns its original outcome) holds for `ORDER_RETENTION_HOURS`;
  after that the outcome is `410 transport-request-expired`, never a new acceptance. A `transport_request_id` is
  never reusable, whatever its age: a rejected id answers its `422` forever (ruling P4-R21 is unbounded, ADR 0005)
  and an accepted one answers its view, then the 410. The caller mints a new id per attempt.
- A late subsystem fact for a purged order's leg finds no leg reference and is ignored as foreign, exactly as a fact
  for a leg this context never owned.
- A container in clarification, or one occupying a station after its order ended, keeps that order until it is
  released. The purge never breaks the hold or the slot.
- The flow lock is held for one order batch at a time: lower `PURGE_BATCH` if a batch ever shows in the
  writers' lock waits.
