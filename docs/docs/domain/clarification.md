---
id: clarification
title: "Clarification"
sidebar_label: "Clarification"
description: "Which facts send a container to clarification, what happens to its transport, and how a clarification is resolved."
---

# Clarification (decision D12)

## What sends a container to clarification

| Fact | Reason | The transport |
| --- | --- | --- |
| asrs-control `ContainerPresentedAtPort` with `scan_result` `NO_READ` | `NO_READ` | fails `CLARIFICATION` |
| `ContainerPresentedAtPort` with `scan_result` `MISMATCH` | `MISMATCH` (with `scanned_container_id`) | fails `CLARIFICATION` |
| asrs-control `StorageJobFailed` `CONTAINER_NOT_AT_PORT` | `CONTAINER_NOT_AT_PORT` | fails `CLARIFICATION` |
| `StorageJobFailed` `STORAGE_MAP_MISMATCH` | `STORAGE_MAP_MISMATCH` | fails `CLARIFICATION` |
| amr-fleet-control `LegFailed` `PICK_FAILED` | `CONTAINER_NOT_AT_SOURCE` | fails `CLARIFICATION` |
| a leg that failed with the container on the robot (`LegFailed` `container_on_robot` true, a `DROP_FAILED`, a `PICKED_UP` leg timed out) | `CONTAINER_ON_FAILED_ROBOT` | fails `SUBSYSTEM_FAULT` (stays `CANCELLED` when the failure was the cancellation it asked for) |

Each puts the container's track on hold and publishes `ContainerSentToClarification` (`reason`,
`detected_at_location`, `scanned_container_id`?, `clarification_point`?, `sent_at`). One container is one exception
case: a second fact on a held track keeps the first hold, its reason and its `since`, and publishes nothing
(ruling P4-R5).

## Freeing an AS/RS output port

A container held while standing on an AS/RS output port blocks that port. An INTERNAL order (one `MOVE`, no transport
event) carries it to the first clarification point of the layout, so the port is freed; `ContainerSentToClarification`
then names the `clarification_point`, and `transport_order_id` may name the internal move while
`transport_request_id` is absent.

## While held

A held container is never dispatched: a new request for it is rejected `CONTAINER_BUSY` (the detail says it is held at
clarification). exception-management opens a case from `ContainerSentToClarification`.

## Resolution

`POST /container-tracks/{containerId}/clarification-resolution` (an operator for now; exception-management later),
with an optional `location` naming where the container now is (default: where the track is). `200` answers the track;
`409 not-in-clarification` and `404 container-track-not-found` are RETRYABLE; `422 unknown-location` is FINAL.
