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
3. Replay classes: FINAL (malformed, purpose rule, conflict, `station-not-found`, unknown location), OUTCOME (the
   four rejections: the rejection record and `TransportRejected` commit WITH the stored answer — the middleware's
   KeepWrites path), RETRYABLE (cancellation and clarification refusals that depend on state, the two not-founds
   below, 5xx: key released).
   Amended (ruling P4-R12): `transport-request-not-found` (cancellation) and `container-track-not-found`
   (clarification resolution) are RETRYABLE, not FINAL. The fleet rule makes a 404 on a command's own target FINAL
   only when the service mints the id and never deletes it. Here the caller mints `transport_request_id`, and a
   `ContainerStored` fact or an acceptance creates a track, so either target can exist by the retry: a FINAL 404
   would pin a cancellation that raced ahead of its request (or was retried after a request timeout) for 24 h
   while the transport runs to completion.
   Amended (ruling P4-R21): all four rejections stay OUTCOME, `SUBSYSTEM_UNAVAILABLE` and `UNKNOWN_CONTAINER`
   included, although both depend on state a retry can change. The rejection is also the published fact
   `TransportRejected`, and a `transport_request_id` has exactly one outcome: a released key would let a retry of the
   same id accept after its rejection was published. The caller mints a NEW `transport_request_id` per attempt
   (task id + attempt counter) and never retries a rejected id; ruling P4-R20 (no POST decided before the subsystem
   backlog sampled at boot is replayed) keeps these rejections from being decided on a previous run's facts.
4. Cancellation `POST /transport-requests/{id}/cancellation`: `200` cancelled now, `202` asked of the subsystem,
   `409 transport-not-cancellable` (on a robot, or waiting on an AS/RS output port), `409 transport-ended`.
5. Read routes (`GET` request, track, station, subsystems) exist for operators and agents, never for polling. No
   route or body names a robot, leg, node or storage job.

## Consequences

- plan 05b's adapter treats `422` as final for that id and sends a new id (task id + attempt) to try again: it mints
  a NEW `transport_request_id` (and a new `Idempotency-Key`) for every attempt and never retries a rejected id, also
  after `SUBSYSTEM_UNAVAILABLE` or `UNKNOWN_CONTAINER` (ruling P4-R21).
- A rejection is both an answer and a fact: the WES may act on either, and both say the same thing.
- Lock order (Task 13 review): the Idempotency middleware's unit of work is the outermost one; the use case takes
  the flow lock first and the key row is written only after the handler returned (a FINAL refusal's row on its
  own, after the rollback), so no request holds a key row while it waits for the flow lock. Two requests under
  one key that both pass the Lookup before either committed are serialized by the flow lock: the second runs,
  loses the key at save time and rolls back everything it wrote, answering the first answer (same body) or
  `409 idempotency-key-reused` (another body). The narrow cost: the loser's use case executes once, invisibly.
  A FINAL refusal is resolved the same way by `finishRefused`, save then re-read: its row is saved on its own after
  the rollback and the key is read back, so when a concurrent request stored its answer first, this request answers
  that stored answer (same body) or `409 idempotency-key-reused` (another body), never its own refusal.
- Releasing a key (a RETRYABLE refusal, a 5xx, a panic) stores nothing, so the same key with ANOTHER body afterwards
  is a NEW request that executes, not `409 idempotency-key-reused`.
- An unstored refusal re-reads the key before answering (ruling P4-R16): a duplicate that passed the Lookup while
  the first copy was in flight waits on the flow lock and decides only after the first copy committed its answer;
  when it reaches a RETRYABLE refusal (the hold already lifted, the transport already ended), `finishRefused` looks
  the key up again and replays the stored answer (another body: `409 idempotency-key-reused`). N in-flight
  duplicates are one run and N-1 replays. A failed re-read is logged at WARN and the request's own refusal goes out.
- A not-found on a keyed POST is RETRYABLE (decision 3, ruling P4-R12): a cancellation that raced ahead of its
  request, or a resolution sent before the container's first fact, answers `404` now and executes when retried once
  the target exists, instead of replaying the `404` for 24 hours.
