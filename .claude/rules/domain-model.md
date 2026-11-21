# Domain model: ubiquitous language, aggregates, events, use cases

## Ubiquitous Language (use these exact names)

- **TransportRequest** — the WES's request to move one container: source, destination, purpose, priority, due.
  Identified by `transport_request_id`; its record keeps the ONE outcome (accepted with an order, or rejected).
- **TransportOrder** — this context's accepted plan for one request: legs in order and one outcome. Not a
  customer order and not a VDA 5050 order. An INTERNAL order (a clarification move) publishes no transport event.
- **Leg** — one move inside one subsystem from one handoff point to the next: `RETRIEVE` or `STORE` (asrs-control),
  `MOVE` (amr-fleet-control). Its reference `<orderId>.<seq>.<attempt>` is the subsystem's `reference`.
- **HandoffPoint** — where a container passes between subsystems: an AS/RS port, an AMR pickup or drop node.
- **Place** — anything a request can name: the AS/RS (its logical location code), a station (id or location code),
  a handoff (decant, pack, dock, forward), a clarification point, an AS/RS port.
- **ContainerTrack** — the physical position of one container and its history inside the automation.
- **StationQueue** — the occupants and the waiting arrivals of one goods-to-person station, under its buffer limit.
- **ClarificationPoint** — where a container goes when it cannot be identified or does not match its order.
- **Purpose** — `PICK`, `PUTAWAY`, `REPLENISH`, `OUTBOUND`, `RETURN`. **Priority** — `NORMAL`, `URGENT`.
- **Subsystem** — the AMR fleet or one AS/RS. **SubsystemCapacity** — one snapshot per subsystem and station.
- **Command** — one queued call to a subsystem (`REQUEST_LEG`, `CANCEL_LEG`, `CONFIRM_PICKUP`).

## Aggregates

| Aggregate (package) | Invariants it protects |
| --- | --- |
| `transportrequest.Record` | 6: one outcome per `transport_request_id`; a retry with the same request returns it |
| `transportorder.Order` | 2: a leg starts only after the previous leg's handoff fact; 5: exactly one of COMPLETED / FAILED, a failure carries a reason |
| `containertrack.Track` | 1: at most one active order per container; 4: never dispatched while in clarification |
| `stationqueue.Queue` | 3: occupants never exceed the buffer; admission URGENT first, then due time |

Pure policies: `layout` (places and handoff points), `routing` (places → leg plans), `capacity` (snapshots).

## Domain events

`transportrequest`: TransportRejected. `transportorder`: TransportAccepted, ContainerArrivedAtStation,
TransportCompleted, TransportFailed. `containertrack`: ContainerSentToClarification. `subsystemcapacity`:
SubsystemCapacityChanged. Full types in ADR 0003; never invent a type outside that list.

## Key use cases (internal/application/usecases, one `Service`)

RequestTransport, CancelTransport, ResolveClarification; OnContainerStored, OnContainerPresented,
OnStorageJobFailed, OnEquipmentStateChanged, OnAsrsCapacityChanged; OnLegAssigned, OnContainerPickedUp,
OnLegCompleted, OnLegFailed, OnFleetCapacityChanged; DispatchDue; Tick; queries Transport, Track, Station,
Subsystems, Order (internal view).

## Conventions

- Every write goes through `Service.write` (unit of work + flow lock); every Kafka fact through `Service.consume`
  (inbox claim in the same unit of work).
- A fact for an unknown reference changes no order; a physical fact still moves the container's track.
- Tests assert exact statuses, reasons, positions and queue order.
