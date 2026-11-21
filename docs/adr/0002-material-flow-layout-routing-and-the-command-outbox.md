# ADR 0002: The material-flow layout, routing through handoff points, and the subsystem command outbox

## Status

Accepted (2026-11-20).

## Context

A transport request names a container, a source and a destination; the WCS "chooses the route" (seam decision 3).
Responsibility 2: "A leg starts **only after** the previous leg's handoff is confirmed by the subsystem that holds
the container." The subsystems are called over REST (relationships 4 and 5) and report progress only as events.

## Decision

1. **Layout file.** `MFC_LAYOUT_FILE` (YAML): `siteId`; `fleet` (`fleetId`, `legsPerRobotHour`); `asrs[]` with the
   AS/RS logical `locationCode` and its ports (`portId`, `kind`, `locationCode`, `handoffPointId`); `stations[]`
   (`stationId`, `locationCode`?, `handoffPointId`, `bufferLimit`); `handoffs[]` (`locationCode`, `handoffPointId`,
   `kind`); `clarificationPoints[]`. Location codes follow facility-layout's AS/RS convention (plan 05a D14). Ids
   must equal asrs-control's layout and amr-fleet-control's map; the three files change together.
2. **Routing** (`internal/domain/routing`, pure): AS/RS → AMR place = RETRIEVE then MOVE; AMR place → AS/RS = MOVE
   then STORE; AMR place → AMR place = MOVE; anything else is unreachable. The output port of a retrieve is the one
   asrs-control presents at; the input port of a store is chosen when the MOVE is dispatched
   (`recommended_input_port_id`, else the layout's first input port).
3. **Legs are matched by our reference.** Each leg attempt has the reference `<orderId>.<seq>.<attempt>`, sent as
   the subsystem request's `reference` and used as the Idempotency-Key base. Facts are matched by it; the
   subsystem's own id is recorded only for cancellation.
4. **Command outbox.** No unit of work calls a subsystem. A use case inserts a `subsystem_commands` row
   (`REQUEST_LEG`, `CANCEL_LEG`, `CONFIRM_PICKUP`) in its unit of work; one dispatcher goroutine calls the
   subsystem with the command's Idempotency-Key outside any transaction and applies the answer in a new unit of
   work. A RETRYABLE refusal, a 5xx or a timeout is retried with the same key (1 s → 30 s) until 300 s after the
   first attempt; a FINAL refusal fails the transport at once.
5. **Flow lock.** Every write unit of work first locks the single `flow_lock` row, so a REST request, a Kafka
   fact, a dispatcher answer and the tick never interleave.
6. **Snapshot evolution** (added after the Task 13 review). Aggregates and the other stored values are persisted
   as the JSON of their Go type (`transport_requests.record`, `transport_orders.snapshot`,
   `container_tracks.snapshot`, `container_moves.move`, `station_queues.snapshot`, `subsystem_capacities.snapshot`,
   `equipment_units.state`; exported field names, no tags in the domain). The stored shape evolves **additively
   only**: a new field is added with a zero value that means "absent", so a row written before it decodes unchanged.
   Renaming or retyping a field, or changing what it means, is a numbered migration that rewrites the affected
   JSONB columns in the same change; it is never a code-only edit. A rollback to an older binary decodes a newer row
   and ignores the fields it does not know, and its next save of that aggregate DROPS them: rolling back across an
   additive change loses the new fields of every aggregate the old binary rewrites, and rolling back across a
   rename or retype migration needs that migration's down step.

## Consequences

- A crash between commit and call loses nothing: the command is still pending and its key makes the retry safe.
- A subsystem fact can arrive before the dispatcher recorded the subsystem's `202`; matching by our reference makes
  the order of the two irrelevant.
- Writes are serial. Throughput is bounded by one Postgres transaction per fact, far above the floor's event rate.
- `internal/adapters/outbound/postgres/testdata/snapshot_keys.golden` pins the sorted key paths and leaf types of
  every stored type: an added path is regenerated, a renamed, retyped or removed one fails the test until its
  migration exists.
