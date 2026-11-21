---
id: domain-events
title: "Domain events"
sidebar_label: "Domain events"
description: "The domain events material-flow-control raises, which aggregate raises each, and what it means."
---

# Domain events

| Event | Raised by | Means |
| --- | --- | --- |
| `TransportRejected` | `TransportRequest` | the request was refused at once (`UNKNOWN_CONTAINER`, `CONTAINER_BUSY`, `DESTINATION_UNREACHABLE`, `SUBSYSTEM_UNAVAILABLE`); no order exists |
| `TransportAccepted` | `TransportOrder` | an order was planned (`legs_planned`); the first leg is requested after commit |
| `ContainerArrivedAtStation` | `TransportOrder` | the container reached its goods-to-person station at `queue_position`; the WES may start the pick |
| `TransportCompleted` | `TransportOrder` | the container is at the destination; the transport's one outcome |
| `TransportFailed` | `TransportOrder` | the transport ended without its destination (`CLARIFICATION`, `SUBSYSTEM_FAULT`, `CANCELLED`, `TIMEOUT`); the one outcome |
| `ContainerSentToClarification` | `ContainerTrack` | a container is held and must be checked; exception-management opens a case |
| `SubsystemCapacityChanged` | capacity snapshot | the capacity of the AMR fleet, the AS/RS or a station changed |

Every field, type and subject is on [Events published](/docs/ecosystem/events-published). An internal order (a
clarification move) raises no transport event.
