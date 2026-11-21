---
id: replay-classes
title: "Replay classes"
sidebar_label: "Replay classes"
description: "How the Idempotency-Key answers a retry: the FINAL, OUTCOME and RETRYABLE classes, the full REST problem catalogue and the corner cases."
---

# Replay classes

Every POST of the relationship-1 interface runs inside the `Idempotency` middleware
(`internal/adapters/inbound/http/idempotency.go`, [ADR 0005](/docs/adr/0005-the-relationship-1-rest-interface-and-replay-classes)),
and every problem type declares one replay class in `internal/adapters/inbound/http/errors.go` (pinned by
`errors_test.go`). The fleet rule is `docs/idempotency-replay-classes.md`.

| Class | Stored under the key? | A retry with the same key and body | Used for |
| --- | --- | --- | --- |
| 2xx | yes, in the request's own transaction | replays the answer with `Idempotency-Replayed: true` | every success |
| FINAL | yes, on its own after the rollback | replays the refusal | an answer the request alone decides |
| OUTCOME | yes, and the request's writes commit WITH it | replays the rejection; nothing new is created | the four rejections: the rejection record and its `TransportRejected` commit with the stored answer |
| RETRYABLE | no: the key is released | executes again | an answer that depends on state the retry can change, 5xx, panics |

## The REST problem catalogue

| Code | Status | Class | Raised by |
| --- | --- | --- | --- |
| `bad-request` | 400 | FINAL | a body that is not valid JSON for the operation (or above 64 KiB) |
| `invalid-parameter` | 400 | FINAL | a field rule, source = destination, a purpose rule (decision D4), a bad path id |
| `idempotency-key-required` | 400 | FINAL | a POST without `Idempotency-Key`, or a key longer than 128 characters |
| `idempotency-key-reused` | 409 | FINAL | the key was stored for another method, path or body |
| `transport-request-conflict` | 409 | FINAL | the `transport_request_id` was used with another request |
| `unknown-location` | 422 | FINAL | a clarification resolution naming a place outside the layout |
| `station-not-found` | 404 | FINAL | `GET /stations/{stationId}` (the layout decides it) |
| `unknown-container` | 422 | OUTCOME | rejection `UNKNOWN_CONTAINER` |
| `container-busy` | 422 | OUTCOME | rejection `CONTAINER_BUSY` (an active transport, or held at clarification) |
| `destination-unreachable` | 422 | OUTCOME | rejection `DESTINATION_UNREACHABLE` |
| `subsystem-unavailable` | 422 | OUTCOME | rejection `SUBSYSTEM_UNAVAILABLE` |
| `transport-request-not-found` | 404 | RETRYABLE | a cancellation (or a `GET`) for an id that has no record yet |
| `container-track-not-found` | 404 | RETRYABLE | a resolution (or a `GET`) for a container this context has not seen yet |
| `transport-not-cancellable` | 409 | RETRYABLE | the container is on a robot or waiting on an AS/RS output port |
| `transport-ended` | 409 | RETRYABLE | the transport has ended, or the request was rejected |
| `not-in-clarification` | 409 | RETRYABLE | the container is not held |
| `internal-error` | 500 | RETRYABLE | any untyped error or a panic (never stored) |
| `dependency-unavailable` | 503 | RETRYABLE | `GET /readyz` while Postgres or Kafka is down |

Every 422 carries `reason` and `transport_request_id`. A `GET` is not keyed: its class only matters where the same
code also answers a POST.

### Why the two not-founds are RETRYABLE (ruling P4-R12)

The fleet rule makes a 404 on a command's own target FINAL only when the service mints the id and never deletes it.
Here the caller mints `transport_request_id`, and a `ContainerStored` fact or an acceptance creates a track, so the
target can exist by the retry: a FINAL 404 would pin a cancellation that raced ahead of its request (or was retried
after a request timeout) for 24 hours while the transport runs to completion. Refuse under key K, create the target,
retry K: the retry executes (`202` or `200`), it is not a replay.

## Corner cases

- **The fingerprint** is the method, the escaped path and the raw body. The same key on another route or with another
  body is `409 idempotency-key-reused`, as long as an answer is stored under it.
- **A released key is free.** A RETRYABLE refusal, a 5xx or a panic stores nothing, so the same key with ANOTHER body
  afterwards is a NEW request that executes, not `idempotency-key-reused`.
- **Two copies in flight.** Two requests under one key that both pass the Lookup before either committed are
  serialized by the flow lock. For a 2xx or an OUTCOME the second runs, loses the key at save time, rolls back
  everything it wrote and answers the first answer (same body) or `409 idempotency-key-reused` (another body); the
  cost is one invisible execution of its use case.
- **A FINAL refusal is saved, then re-read.** Its row is saved on its own after the rollback and the key is read back;
  when a concurrent request stored first, the stored answer goes out instead of this request's refusal.
- **An unstored refusal re-reads the key before answering** (ruling P4-R16). A duplicate that waited on the flow lock
  decides only after the first copy committed; when it reaches a RETRYABLE refusal (the hold already lifted, the
  transport already ended), it looks the key up again and replays the stored answer, so N in-flight duplicates are
  one run and N-1 replays. A failed re-read is logged at WARN
  (`idempotency: re-read before an unstored refusal failed`) and the request's own refusal goes out.
- **The window is 24 hours.** A stored answer older than that is not replayed (checked on every lookup) and the hourly
  purge deletes it from `idempotency_keys`.

## The two layers

The `Idempotency-Key` sits on top of the domain key: a retry of the same `transport_request_id` and request under a
NEW key still returns the original outcome and creates nothing (responsibility 6), and a rejected id stays rejected.
