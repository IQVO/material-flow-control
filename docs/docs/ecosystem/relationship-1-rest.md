---
id: relationship-1-rest
title: "The relationship-1 interface"
sidebar_label: "Relationship 1 (REST)"
description: "The REST interface fulfillment-execution calls: routes, bodies, refusals and their replay classes, the views, the facts back and the rules for the caller."
---

# The relationship-1 interface

WCS context map relationship 1: fulfillment-execution's `EquipmentCommandPort` adapter (plan 05b) is the customer; this
context is the Open Host Service with a Published Language ([ADR 0005](/docs/adr/0005-the-relationship-1-rest-interface-and-replay-classes)).
The contract is `apis/openapi.yaml`; the generated pages are in the [API reference](/docs/api-reference).

Base path: the Service `material-flow-control` on :8080 (in-cluster `http://material-flow-control.warehouse-systems.svc.cluster.local`), through Kong at `/api/material-flow-control`. Every POST requires `Idempotency-Key` (1-128 chars); a replayed answer carries `Idempotency-Replayed: true`. Bodies snake_case; errors RFC 9457 `application/problem+json` with a stable `code`. No authentication. Location vocabulary: the AS/RS is addressed by its logical `LocationCode` (`WH1-AUTO-ASRS-ASRS1-00-00-S`, plan 05a D14); a goods-to-person station by its station id (`ST1`) or its `LocationCode`; any other place (decant, pack station, dock door, forward-area handoff, clarification point) by its `LocationCode`.

| Method and path | Use case | Body | Success | Refusals (code → status, replay class) |
| --- | --- | --- | --- | --- |
| `POST /transport-requests` | `RequestTransport` | `{"transport_request_id", "container_id", "source", "destination", "purpose", "priority", "due_by"}` — all required; `purpose` ∈ `PICK`, `PUTAWAY`, `REPLENISH`, `OUTBOUND`, `RETURN`; `priority` ∈ `NORMAL`, `URGENT`; `due_by` RFC 3339; ids 1-128 chars without control characters, `container_id` 1-64 chars `^[A-Za-z0-9._-]+$` (the LPN rule) | `202` `TransportOrder` + `Location: /transport-requests/{transportRequestId}`; a retry of the same `transport_request_id` and request answers the original outcome (`202` with the current view, or the same `422`) | `bad-request` 400 F, `invalid-parameter` 400 F (field rules, source = destination, purpose rule D4), `transport-request-conflict` 409 F (same id, other request), `unknown-container` 422 O, `container-busy` 422 O, `destination-unreachable` 422 O, `subsystem-unavailable` 422 O — every 422 carries `reason` (`UNKNOWN_CONTAINER`, `CONTAINER_BUSY`, `DESTINATION_UNREACHABLE`, `SUBSYSTEM_UNAVAILABLE`) and `transport_request_id` |
| `GET /transport-requests/{transportRequestId}` | `Transport` | — | `200` `TransportOrder` (status `REJECTED` with `reason` for a rejected id) | `invalid-parameter` 400, `transport-request-not-found` 404 |
| `POST /transport-requests/{transportRequestId}/cancellation` | `CancelTransport` | none (empty body) | `200` `TransportOrder` status `FAILED` reason `CANCELLED`; `202` `TransportOrder` status `CANCEL_REQUESTED` | `transport-request-not-found` 404 R, `transport-not-cancellable` 409 R, `transport-ended` 409 R |
| `GET /container-tracks/{containerId}` | `Track` | — | `200` `ContainerTrack` | `invalid-parameter` 400, `container-track-not-found` 404 |
| `POST /container-tracks/{containerId}/clarification-resolution` | `ResolveClarification` | `{"location"?}` (a place reference; default: where the track is) | `200` `ContainerTrack` | `bad-request` 400 F, `container-track-not-found` 404 R, `unknown-location` 422 F, `not-in-clarification` 409 R |
| `GET /stations/{stationId}` | `Station` | — | `200` `StationQueue` | `invalid-parameter` 400, `station-not-found` 404 |
| `GET /subsystems` | `Subsystems` | — | `200` `{"subsystems": [SubsystemCapacity…]}` sorted by kind, id | — |
| `GET /healthz`, `GET /readyz` | probes | — | `200` | `503` while draining, while a dependency (Postgres, Kafka) is down, or from boot until both subsystem consumers caught up with the backlog sampled at boot (`subsystem-facts`; every POST then answers `503` `dependency-unavailable`, RETRYABLE) |

Common: `idempotency-key-required` 400 F, `idempotency-key-invalid` 400 F (not 1-128 printable ASCII), `idempotency-key-reused` 409 F, `internal-error` 500 R (never stored), `dependency-unavailable` 503 R. F = FINAL (stored, replayed), O = OUTCOME (the rejection record and `TransportRejected` commit with the stored answer; replayed), R = RETRYABLE (key released, a retry executes).

`TransportOrder`: `transport_request_id`, `transport_order_id`? (absent when rejected), `container_id`, `source`, `destination`, `purpose`, `priority`, `due_by`, `status` (`IN_PROGRESS`, `CANCEL_REQUESTED`, `COMPLETED`, `FAILED`, `REJECTED`), `reason`? (`CLARIFICATION`, `SUBSYSTEM_FAULT`, `CANCELLED`, `TIMEOUT`; or a rejection reason when `REJECTED`), `container_location`? (the container's last confirmed location: a location code or station id; absent while on a robot or unknown), `legs_total`, `legs_done`, `accepted_at`? , `rejected_at`?, `ended_at`?. `ContainerTrack`: `container_id`, `position` (`kind` ∈ `IN_ASRS`, `AT_PORT`, `ON_ROBOT`, `AT_STATION`, `AT_HANDOFF`, `AT_CLARIFICATION`, `UNKNOWN`; `code`: the location code, station id or — for `ON_ROBOT` — the fleet id), `active_transport_request_id`?, `clarification`? (`reason`, `detected_at_location`, `scanned_container_id`?, `since`), `updated_at`, `history` (last 20 moves: `from`, `to`, `transport_request_id`?, `at`). `StationQueue`: `station_id`, `buffer_limit`, `occupied`, `occupants` (`container_id`, `transport_request_id`, `position`, `admitted_at`), `waiting` (`container_id`, `transport_request_id`, `priority`, `due_by`, `ready_at`, in admission order). `SubsystemCapacity`: `subsystem_kind`, `subsystem_id`, `status`, `units_total`, `units_available`, `open_work`, `capacity_per_hour`, `buffer_limit`, `occupied`, `queue_depth`, `measured_at`.

Facts back on `warehouse.material-flow-control.events` (data fields; `*_at` this context's clock): TransportAccepted (`transport_order_id`, `transport_request_id`, `container_id`, `source`, `destination`, `purpose`, `priority`, `due_by`, `legs_planned`, `accepted_at`); TransportRejected (`transport_request_id`, `container_id`, `source`, `destination`, `purpose`, `reason`, `detail`, `rejected_at`); ContainerArrivedAtStation (`transport_order_id`, `transport_request_id`, `container_id`, `station_id`, `purpose`, `priority`, `due_by`, `queue_position`, `arrived_at`); TransportCompleted (`transport_order_id`, `transport_request_id`, `container_id`, `destination`, `purpose`, `completed_at`); TransportFailed (`transport_order_id`, `transport_request_id`, `container_id`, `purpose`, `reason`, `subsystem_reason`?, `container_location`?, `failed_at`); ContainerSentToClarification (`container_id`, `transport_order_id`?, `transport_request_id`?, `reason` ∈ `NO_READ`, `MISMATCH`, `CONTAINER_NOT_AT_SOURCE`, `CONTAINER_NOT_AT_PORT`, `STORAGE_MAP_MISMATCH`, `CONTAINER_ON_FAILED_ROBOT`, `scanned_container_id`?, `detected_at_location`, `clarification_point`? (set when the container is moved there), `sent_at`); SubsystemCapacityChanged (`subsystem_kind`, `subsystem_id`, `status`, `units_total`, `units_available`, `open_work`, `capacity_per_hour`, `buffer_limit`, `occupied`, `queue_depth`, `measured_at`).

Rules for the caller (plan 05b): mint a NEW `transport_request_id` for every attempt, derived from the task id AND an attempt counter (a rejected id stays rejected, D7, ruling P4-R21); treat `422` as final for that id and never retry a rejected id; never poll — drive the task from `TransportAccepted`, `ContainerArrivedAtStation`, `TransportCompleted`, `TransportFailed`; dispatch on the full CloudEvents `type`, dedupe on `id`; for `RETURN` write no stock transaction (seam decision 4).

## Why every rejection is OUTCOME, `SUBSYSTEM_UNAVAILABLE` and `UNKNOWN_CONTAINER` included (ruling P4-R21)

Two of the four rejections depend on state that can change by a retry: `SUBSYSTEM_UNAVAILABLE` (a subsystem on the
route reads `UNAVAILABLE`) and `UNKNOWN_CONTAINER` (no `ContainerStored` fact or track for the container yet). They
still stay OUTCOME, not RETRYABLE: the `REJECTED` record commits and `TransportRejected` is published with the stored
answer, and a `transport_request_id` has exactly ONE outcome (responsibility 6). Releasing the key would let a retry
of the same id accept after a `TransportRejected` for it was already published, so the WES would see two outcomes for
one request. Instead:

- the caller (fulfillment-execution, plan 05b) mints a NEW `transport_request_id` per attempt (task id + attempt
  counter) and sends it under a NEW `Idempotency-Key`; it never retries a rejected id, and a retry of the same id
  (a lost answer, a timeout) replays the same `422`;
- the window in which these two rejections would be decided on stale state at boot is closed by ruling P4-R20: until
  both subsystem consumers have replayed the backlog sampled at boot, every POST answers `503`
  `dependency-unavailable` (RETRYABLE, key released) and `/readyz` is not ready, so a request is never rejected on
  the facts of a previous run.

The replay classes, the stored-answer rules and the corner cases (a released key, in-flight duplicates) are on the
[Replay classes](/docs/development/replay-classes) page.
