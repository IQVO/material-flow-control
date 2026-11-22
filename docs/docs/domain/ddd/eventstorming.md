---
id: eventstorming
title: "EventStorming"
sidebar_label: "EventStorming"
description: "The EventStorming board of material-flow-control: commands, aggregates, events and policies."
---

# EventStorming

| Actor or trigger | Command | Aggregate | Event | Policy that follows |
| --- | --- | --- | --- | --- |
| WES | RequestTransport | TransportRequest, TransportOrder, ContainerTrack | `TransportAccepted` / `TransportRejected` | request the first leg |
| WES | CancelTransport | TransportOrder | `TransportFailed` (`CANCELLED`) or a `CANCEL_LEG` | a refused cancel lets the transport run |
| asrs-control | ContainerStored | ContainerTrack, TransportOrder | (`TransportCompleted` for a STORE) | the container is known `IN_ASRS` |
| asrs-control | ContainerPresentedAtPort | TransportOrder, ContainerTrack, StationQueue | (`ContainerSentToClarification` on NO_READ/MISMATCH) | join the station queue; request the MOVE |
| asrs-control | StorageJobFailed | TransportOrder, ContainerTrack | `TransportFailed` / `ContainerSentToClarification` | re-plan a STORE once |
| amr-fleet-control | LegAssigned, ContainerPickedUp | TransportOrder, ContainerTrack, StationQueue | | confirm the port pickup; free the station slot |
| amr-fleet-control | LegCompleted | TransportOrder, ContainerTrack | `ContainerArrivedAtStation`, `TransportCompleted` | start the next leg |
| amr-fleet-control | LegFailed | TransportOrder, ContainerTrack | `TransportFailed` / `ContainerSentToClarification` | retry up to `LEG_RETRY_LIMIT` |
| either subsystem | capacity fact | capacity snapshot | `SubsystemCapacityChanged` | publish on change only |
| clock | Tick | TransportOrder | `TransportFailed` (`TIMEOUT`) | best-effort `CANCEL_LEG` |
| operator | ResolveClarification | ContainerTrack | | the container is dispatchable again |

Hot spots recorded as decisions: the station buffer owner (ADR 0001), the event segment (ADR 0003), capacity
measurement (ADR 0006).
