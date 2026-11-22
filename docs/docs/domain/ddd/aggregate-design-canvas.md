---
id: aggregate-design-canvas
title: "Aggregate design canvas"
sidebar_label: "Aggregate design canvas"
description: "The ddd-crew aggregate design canvas of the four aggregates."
---

# Aggregate design canvas

| | `TransportRequest` | `TransportOrder` | `ContainerTrack` | `StationQueue` |
| --- | --- | --- | --- | --- |
| **Description** | the one outcome of a `transport_request_id` | an accepted request's legs and outcome | one container's physical position | one station's occupants and waiting arrivals |
| **State transitions** | none (written once: ACCEPTED or REJECTED) | `IN_PROGRESS` to `COMPLETED` or `FAILED`; legs `PLANNED` to `DONE`/`FAILED`/`CANCELLED` | position moves; reserved/released; held/resolved | join, admit, leave, drop |
| **Enforced invariants** | 6 | 2, 5 | 1, 4 | 3 |
| **Corrective policies** | a rejected id stays rejected: the WES sends a new id | retry a leg, re-plan a STORE once, fail with a reason | a held track rejects requests `CONTAINER_BUSY` | a full station keeps arrivals waiting upstream |
| **Handled commands** | RequestTransport | RequestTransport, CancelTransport, the ten facts, DispatchDue, Tick | RequestTransport, ResolveClarification, the facts | the facts that make a container ready, leave or fail |
| **Created events** | `TransportRejected` | `TransportAccepted`, `ContainerArrivedAtStation`, `TransportCompleted`, `TransportFailed` | `ContainerSentToClarification` | (none; its state feeds `SubsystemCapacityChanged`) |
| **Throughput** | one per WES request | a few facts per leg | one move per fact | one change per arrival and departure |
| **Size** | small | legs of one route (2 to 4, plus retries) | last 20 moves served (`container_moves`) | bounded by the buffer limit plus the waiting backlog |

All four change in the same unit of work under the flow lock, so a cross-aggregate rule (an order and its track and
its station) holds without sagas. See [Aggregates](/docs/domain/aggregates).
