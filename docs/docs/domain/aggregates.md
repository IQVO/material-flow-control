---
id: aggregates
title: "Aggregates and invariants"
sidebar_label: "Aggregates"
description: "The four aggregates, the six responsibilities they protect, and the pure layout, routing and capacity packages."
---

# Aggregates and invariants

Every write runs in ONE Postgres unit of work that first takes the flow lock, so the aggregates below change
together and serially ([Architecture](/docs/overview/architecture)).

| Aggregate | Package | Protects | How |
| --- | --- | --- | --- |
| `TransportRequest` (`Record`) | `internal/domain/transportrequest` | 6: a request is idempotent on its `transport_request_id` | The first answer for an id is recorded: `ACCEPTED` with its order id or `REJECTED` with its reason. The same id with the same request (container, source, destination, purpose, priority, due) returns the original outcome and creates nothing; another request under the id is `409 transport-request-conflict`; a rejected id stays rejected. The record is never deleted (ruling P4-R22): after the retention purged an accepted request's ended order, the record answers `410 transport-request-expired` and the id is never accepted again. |
| `TransportOrder` (`Order`) | `internal/domain/transportorder` | 2: a leg starts only after the previous handoff was confirmed; 5: exactly one outcome with a reason | `Next` starts a leg only when the previous one is `DONE`; a `FAILED` leg is skipped only once it was replaced (ruling P4-R7); `Complete` and `Fail` end the order once. |
| `ContainerTrack` (`Track`) | `internal/domain/containertrack` | 1: at most one active order per container; 4: a held container is never dispatched | `Reserve` refuses a busy or held track; `SendToClarification` holds it (a second hold keeps the first, ruling P4-R5); `Resolve` releases it. |
| `StationQueue` (`Queue`) | `internal/domain/stationqueue` | 3: never above the buffer; arrivals by priority, then due time | `Admit` admits waiting entries while `occupied < bufferLimit`, in admission order. |

## The six responsibilities

1. A container has **at most one active transport order** (`ContainerTrack`).
2. A leg starts **only after** the previous leg's handoff is confirmed by the subsystem that holds the container
   (`TransportOrder`).
3. A station queue **never exceeds** its configured buffer, and arrivals follow the requested priority, then the due
   time (`StationQueue`).
4. A container at a clarification point is **never dispatched** until it is resolved (`ContainerTrack`).
5. Every accepted request ends in **exactly one** of `Completed` or `Failed`, and a failure carries a reason code
   (`TransportOrder`).
6. A request is **idempotent** on its `transport_request_id` (`TransportRequest`).

## Pure policies

| Package | What it is |
| --- | --- |
| `internal/domain/layout` | the material-flow layout (AS/RS, ports, stations, handoffs, clarification points) and `Resolve` of a reference to a place |
| `internal/domain/routing` | source and destination places to leg plans ([Routing and handoffs](/docs/domain/routing-and-handoffs)) |
| `internal/domain/capacity` | capacity snapshots per subsystem and station, and `SubsystemCapacityChanged` ([Capacity](/docs/domain/capacity)) |

Aggregates are persisted as the JSON of their `Snapshot` (JSONB columns) and restored with `Restore`.
