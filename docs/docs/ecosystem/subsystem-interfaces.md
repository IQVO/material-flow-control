---
id: subsystem-interfaces
title: "The subsystem interfaces"
sidebar_label: "Subsystem interfaces"
description: "Relationships 4 and 5: the REST calls material-flow-control makes to amr-fleet-control and asrs-control, and how their answers are classified."
---

# The subsystem interfaces (relationships 4 and 5)

Relationships 4 and 5 use the same shape as relationship 1 (`202` + progress on events, `422` refused at once, a
`.../cancellation` sub-resource, every POST idempotent, snake_case). Only the dispatcher calls them, outside any
transaction, with the command's `Idempotency-Key` (the leg reference based); clients live in
`internal/adapters/outbound/asrs`, `internal/adapters/outbound/amr` and the shared `subsystemhttp`.

## asrs-control (relationship 5)

Base `ASRS_CONTROL_URL` (in-cluster `http://asrs-control.warehouse-systems.svc.cluster.local`), contract
`contracts/asrs-control.openapi.yaml`.

| Command | Call | Success |
| --- | --- | --- |
| `REQUEST_LEG` RETRIEVE / STORE | `POST /asrs/{asrsId}/storage-jobs` `{kind, container_id, port_id?, reference}` | `202` `StorageJob` (`storage_job_id`); a RETRIEVE omits `port_id` (asrs-control presents at the first output port of the aisle) |
| `CANCEL_LEG` | `POST /asrs/{asrsId}/storage-jobs/{storageJobId}/cancellation` | `200` / `202` |
| `CONFIRM_PICKUP` | `POST /asrs/{asrsId}/ports/{portId}/pickups` `{container_id}` | `200` |
| (dispatch of a MOVE into the AS/RS) | `GET /asrs/{asrsId}` | `recommended_input_port_id` |

RETRYABLE refusals (retried under the same key): `unknown-container`, `container-busy`, `container-already-stored`,
`storage-full`, `aisle-unavailable`, `port-unreachable`, `port-not-presenting`, `job-not-cancellable`, `job-ended`.

## amr-fleet-control (relationship 4)

Base `AMR_FLEET_CONTROL_URL` (in-cluster `http://amr-fleet-control.warehouse-systems.svc.cluster.local`), contract:
`contracts/amr-fleet-control.openapi.yaml`, pinned from `IQVO/amr-fleet-control` `develop` at `09a8f19`.

| Command | Call | Success |
| --- | --- | --- |
| `REQUEST_LEG` MOVE | `POST /fleets/{fleetId}/legs` `{container_id, from_handoff_point_id, to_handoff_point_id, reference?}` (`reference` omitted when empty) | `202` `Leg` (`leg_id`) |
| `CANCEL_LEG` | `POST /fleets/{fleetId}/legs/{legId}/cancellation` | `200` / `202` |

RETRYABLE refusals: `handoff-unreachable`, `container-busy`, `fleet-unavailable`, `leg-not-cancellable`, `leg-ended`,
`unknown-handoff-point`, `fleet-not-found`, `robot-not-found` (plan 03 ruling P3-R13). A test holds `amr.Retryable`
equal to the RETRYABLE list of the pinned OpenAPI's `info.description`.

## How an answer is classified (decision D14)

| Answer | Handling |
| --- | --- |
| `2xx` with the subsystem's id | the id is recorded on the leg (for cancellation) |
| a RETRYABLE refusal, a `5xx`, `408`, `425`, `429`, a timeout (`SUBSYSTEM_TIMEOUT_MS`), a refused connection, a `2xx` without the id, or a `3xx` (never followed: a redirected POST would become a GET) | retried under the SAME key, backoff 1 s to 30 s, until `LEG_REQUEST_DEADLINE_SECONDS` after the first attempt; then the transport fails `SUBSYSTEM_FAULT` |
| any other refusal (FINAL: `bad-request`, `invalid-parameter`, `asrs-not-found`, `leg-not-found`, `unknown-port`, `wrong-port-kind`, `idempotency-key-reused`) | the transport fails `SUBSYSTEM_FAULT` at once and an ERROR is logged: the layout disagrees with the subsystem |

A problem with a `type` but no `code` takes its code from the type URI's last segment.

## Leg starts and handoffs (decisions D9, D10, D13)

- A leg starts only after the previous leg's outcome fact (`ContainerPresentedAtPort` READ before a MOVE from a
  port; the AMR `LegCompleted` at the input port before a STORE).
- Every leg attempt's `reference` is `<orderId>.<seq>.<attempt>`; facts are matched by it, never by the subsystem's
  id, so a fact that arrives before the `202` was recorded still applies.
- `ContainerPickedUp` from an AS/RS OUTPUT port queues `CONFIRM_PICKUP`: the PLC reports no removal, so this call is
  the only signal that the port is free.
- A retryable AMR leg failure gets a new attempt (new reference, new key) up to `LEG_RETRY_LIMIT`; a failed STORE is
  re-planned once through another input port ([Transport lifecycle](/docs/domain/transport-lifecycle)).
