# ADR 0004: Consuming the subsystem facts of asrs-control and amr-fleet-control

## Status

Accepted

## Context

A leg starts only after the previous handoff was confirmed by the subsystem that held the container
(responsibility 2). Those confirmations arrive as facts on `warehouse.asrs-control.events` and
`warehouse.amr-fleet-control.events`.

## Decision

1. Two fixed-group at-least-once consumers (`MFC_ASRS_CONSUMER_GROUP`, `MFC_AMR_CONSUMER_GROUP`), `FetchMessage`
   then `CommitMessages` after the handler returned nil, `StartOffset` FirstOffset, `CommitInterval` and `MaxWait`
   unset (the fleet's consumer checklist, items 1-12).
2. The inbox claim (`processed_events`, keyed by the CloudEvents id) and every effect commit in ONE unit of work
   under the flow lock.
3. Ten types are consumed, dispatched on the FULL type; every other type is committed past. A malformed payload is
   dead-lettered at once; a transient error retries the same message (200 ms to 5 s, 5 attempts) then dead-letters.
4. Facts are matched by OUR leg reference (`<orderId>.<seq>.<attempt>`), never by the subsystem's id.
5. The consumed contracts are pinned byte copies of the producers' `develop` (`contracts/`), and every pinned
   example of a consumed type decodes through the production handler.

## Consequences

- A new deployment learns every stored container from the topic's retention (ADR 0001's known limit).
- A producer change to a consumed field fails `pinned-contracts` before it reaches this consumer.
