---
id: introduction
title: "Introduction"
sidebar_label: "Introduction"
description: "What material-flow-control owns and does not own, its Supporting classification in the WCS tier, its five capabilities and six guarantees."
---

# material-flow-control

:::caution[A domain-driven design study]
This project is a domain-driven design study. It is not associated with, endorsed by or affiliated with any company,
and it uses no company's code, data, logos or trademarks. All names, figures and examples are illustrative or synthetic.
:::

**material-flow-control** is the material flow controller of the warehouse-systems WCS tier and the ONE door from the
WES to the automation. Its purpose: turn a WES transport request into a completed physical move of a container across
the automated subsystems, and be the one source of truth for **where each container physically is** inside the
automation.

fulfillment-execution asks it to move a container (`POST /transport-requests`); it accepts or rejects the request at
once (`202` or `422`), splits an accepted request into legs through handoff points (an `asrs-control` RETRIEVE or
STORE, an `amr-fleet-control` MOVE), starts each leg only after the previous handoff was confirmed by the subsystem
that held the container, sequences arrivals at each goods-to-person station, and ends every accepted request in
exactly one of `TransportCompleted` or `TransportFailed`.

## Classification

**Supporting** subdomain of the WCS tier (ADR 0001): necessary for the warehouse to run, specific to how this
warehouse moves containers, but not where it competes. CloudEvents segment `wes`; topics
`warehouse.material-flow-control.events` and `warehouse.material-flow-control.analytics`.

## The five capabilities

| Capability | What it does |
| --- | --- |
| Transport order management | Accepts or rejects a request, splits it into legs through handoff points (AS/RS port, then AMR, then station) and tracks it to exactly one outcome. |
| Container tracking | Keeps each container's physical position: in the AS/RS, at an AS/RS port, on a robot, at a station, at a handoff (decant, pack, dock, forward area) or at a clarification point. |
| Station sequencing | Orders arrivals at each goods-to-person station by priority, then due time, and holds the station buffer limit. |
| Clarification and exceptions | Handles unknown or mismatched containers, failed legs and retries; sends a container to a clarification point and raises `ContainerSentToClarification`. |
| Subsystem capacity | Aggregates the capacity facts of `amr-fleet-control` and `asrs-control` with the station queues into `SubsystemCapacityChanged`. |

## Responsibilities (what it guarantees)

1. A container has **at most one active transport order**.
2. A leg starts **only after** the previous leg's handoff is confirmed by the subsystem that holds the container.
3. A station queue **never exceeds** its configured buffer, and arrivals follow the requested priority, then the due time.
4. A container at a clarification point is **never dispatched** until it is resolved.
5. Every accepted request ends in **exactly one** of `Completed` or `Failed`, and a failure carries a reason code.
6. A request is **idempotent** on its `transport_request_id`: a retried request returns the original outcome and
   creates no second order.

## What it does not own

| Concern | Owner |
| --- | --- |
| What is in a container, and the stock ledger | `inventory-storage` |
| Which container moves, and when work is released | `fulfillment-execution`, `wes-work-planning` |
| The destination of a move | The WES request; the WCS chooses only the route |
| Robot paths, traffic, deadlocks and charging | `amr-fleet-control` |
| Rack positions and shuttle and lift scheduling | `asrs-control` |
| The anomaly lifecycle once raised | `exception-management` |

## Read next

- [Architecture](/docs/overview/architecture): the three binaries, the hexagon, the flow lock and the command outbox.
- [Quickstart](/docs/overview/quickstart): a local run and one transport request.
- [The relationship-1 interface](/docs/ecosystem/relationship-1-rest): what the WES calls.
- [Architecture decisions](/docs/adr): ADRs 0001-0007.
