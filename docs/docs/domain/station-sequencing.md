---
id: station-sequencing
title: "Station sequencing"
sidebar_label: "Station sequencing"
description: "How arrivals at a goods-to-person station are queued and admitted under its buffer limit."
---

# Station sequencing (decision D11)

Each goods-to-person station has one `StationQueue` with the layout's `bufferLimit`.

1. **Join.** A transport bound for a station joins that station's queue when its container is ready to move there:
   presented at the AS/RS output port, or at acceptance when the source is an AMR place. Its MOVE waits in
   `WAITING_STATION`.
2. **Admit.** The queue admits waiting entries while `occupied < bufferLimit`, in this order: `URGENT` before
   `NORMAL`, then the earlier `due_by`, then the earlier readiness, then the order id. An admitted leg goes `PENDING`
   and the dispatcher requests it.
3. **Occupy.** An admitted container occupies its slot from admission until a robot picks it up from the station
   (`ContainerPickedUp` from the station's handoff point), or until its inbound transport fails.
4. **Arrive.** On arrival the transport publishes `ContainerArrivedAtStation` with `queue_position`, the container's
   1-based position among the station's occupants, then `TransportCompleted`.

A full station never drops an arrival: the leg waits upstream (failure path "Station buffer full"), and the station's
`SubsystemCapacityChanged` turns `DEGRADED` while a backlog waits.

`GET /stations/{stationId}` returns the queue: `buffer_limit`, `occupied`, `occupants` (`container_id`,
`transport_request_id`, `position`, `admitted_at`) and `waiting` (in admission order).

The buffer limit is this context's configuration ([ADR 0001](/docs/adr/0001-material-flow-control-bounded-context)).
