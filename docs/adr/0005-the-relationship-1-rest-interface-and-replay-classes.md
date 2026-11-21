# ADR 0005: The relationship-1 REST interface and its replay classes

## Status

Accepted

## Context

fulfillment-execution needs an immediate accept or reject (context map "Why a REST command at relationship 1");
progress is reported only through events. Relationships 4 and 5 already use one shape (`202` + `Location`, `422`
refused at once, a `…/cancellation` sub-resource, every POST idempotent).

## Decision

1. `POST /transport-requests` answers `202` with the order view, or `422` with one of the four rejection reasons
   (`UNKNOWN_CONTAINER`, `CONTAINER_BUSY`, `DESTINATION_UNREACHABLE`, `SUBSYSTEM_UNAVAILABLE`) and the
   `transport_request_id`; seam decision 3's camelCase names become snake_case.
2. Two idempotency layers: the fleet `Idempotency-Key` (24 h) and the domain key `transport_request_id`
   (responsibility 6: a retry returns the original outcome; the same id with another request is
   `409 transport-request-conflict`; a rejected id stays rejected).
3. Replay classes: FINAL (malformed, purpose rule, conflict, not found, unknown location), OUTCOME (the four
   rejections: the rejection record and `TransportRejected` commit WITH the stored answer — the middleware's
   KeepWrites path), RETRYABLE (cancellation and clarification refusals that depend on state, 5xx: key released).
4. Cancellation `POST /transport-requests/{id}/cancellation`: `200` cancelled now, `202` asked of the subsystem,
   `409 transport-not-cancellable` (on a robot, or waiting on an AS/RS output port), `409 transport-ended`.
5. Read routes (`GET` request, track, station, subsystems) exist for operators and agents, never for polling. No
   route or body names a robot, leg, node or storage job.

## Consequences

- plan 05b's adapter treats `422` as final for that id and sends a new id (task id + attempt) to try again.
- A rejection is both an answer and a fact: the WES may act on either, and both say the same thing.
- Lock order (Task 13 review): the Idempotency middleware's unit of work is the outermost one; the use case takes
  the flow lock first and the key row is written only after the handler returned (a FINAL refusal's row on its
  own, after the rollback), so no request holds a key row while it waits for the flow lock. Two requests under
  one key that both pass the Lookup before either committed are serialized by the flow lock: the second runs,
  loses the key at save time and rolls back everything it wrote, answering the first answer (same body) or
  `409 idempotency-key-reused` (another body). The narrow cost: the loser's use case executes once, invisibly.
