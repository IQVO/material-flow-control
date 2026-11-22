---
id: use-cases
title: "Use cases"
sidebar_label: "Use cases"
description: "Every use case of the application Service and the Reader, and which adapter drives it."
---

# Use cases

`internal/application/usecases.Service` holds every write; `usecases.Reader` (no write method) the reads the REST,
MCP and A2A adapters share.

| Use case | Driven by | Effect |
| --- | --- | --- |
| `RequestTransport` | `POST /transport-requests` | accept (an order, its legs, the track reserved, `TransportAccepted`, the first `REQUEST_LEG`) or reject (`TransportRejected`, OUTCOME) |
| `CancelTransport` | `POST /transport-requests/{transportRequestId}/cancellation` | cancel now (`TransportFailed` `CANCELLED`), ask the subsystem (`CANCEL_LEG`), or refuse |
| `ResolveClarification` | `POST /container-tracks/{containerId}/clarification-resolution` | release a held track, optionally at a new place |
| `OnContainerStored`, `OnContainerPresented`, `OnStorageJobFailed`, `OnEquipmentStateChanged`, `OnAsrsCapacityChanged` | the asrs-control consumer | track the container, advance or fail the order, clarification, unit states, AS/RS capacity |
| `OnLegAssigned`, `OnContainerPickedUp`, `OnLegCompleted`, `OnLegFailed`, `OnFleetCapacityChanged` | the amr-fleet-control consumer | robot on the leg, container on the robot (and `CONFIRM_PICKUP` at an output port), delivery, retry or failure, fleet capacity |
| `DispatchDue` | the dispatcher loop | send due subsystem commands outside any transaction, apply each answer in a new unit of work |
| `Tick` | the tick loop | fail legs with no progress for `LEG_TIMEOUT_SECONDS` |
| `Transport`, `OrderDetail` | `GET /transport-requests/{transportRequestId}`, MCP `get_transport`, A2A `transport_status` | the request's view (and the legs, for the agents) |
| `Track` | `GET /container-tracks/{containerId}`, MCP `get_container_track`, A2A `container_position` | the track and its last 20 moves |
| `Station` | `GET /stations/{stationId}`, MCP `get_station`, A2A `station_queue` | the queue |
| `Subsystems` | `GET /subsystems`, MCP `list_subsystems`, A2A `subsystem_capacity` | every capacity snapshot |
| `EquipmentUnits` | MCP `list_equipment_units`, A2A `equipment_units` | the AS/RS units' last states (`asrs-not-found` for an AS/RS the layout does not name) |

Every write runs in one unit of work that takes the flow lock first; a consumer's inbox claim commits in the same unit
of work as the fact's effects.
