# ADR 0001: material-flow-control, the WCS material flow controller (Supporting)

## Status

Accepted (2026-11-20).

## Context

The WCS design (warehouse-docs `docs/wcs/`, PR #165) puts three contexts under the WES: two Generic subsystem
controllers (asrs-control, built; amr-fleet-control) and one Supporting material flow controller that faces the WES.
The context page: "Turn a WES transport request into a completed physical move of a container across the automated
subsystems, and be the one source of truth for **where each container physically is** inside the automation."
The implementation handoff leaves one question to this ADR: "Who configures station buffer limits — Either its own
configuration or a `facility-layout` station attribute".

## Decision

1. This repository is the context: classification **Supporting**, CloudEvents segment `wes`, topics
   `warehouse.material-flow-control.events` and `.analytics`. It is the only WCS context the WES addresses
   (context map relationship 1, an Open Host Service with a Published Language; fulfillment-execution's
   `EquipmentCommandPort` is the ACL on the customer side).
2. It owns transport orders, container tracking, station sequencing, clarification and the subsystem capacity view
   (the five capabilities), and guarantees responsibilities 1-6 with four aggregates: `TransportRequest` (6),
   `TransportOrder` (2, 5), `ContainerTrack` (1, 4), `StationQueue` (3).
3. It does not own container contents or the stock ledger (inventory-storage), which container moves or when
   (fulfillment-execution, wes-work-planning), the destination (the WES request), robot paths and traffic
   (amr-fleet-control), rack positions and shuttle scheduling (asrs-control), or the anomaly lifecycle
   (exception-management).
4. **Station buffer limits are this context's configuration** (the layout file of ADR 0002, `stations[].bufferLimit`).
   A buffer is a property of the flow into a station (how many containers its buffer holds), enforced by the
   controller that sequences arrivals; facility-layout models the building, has no station-buffer attribute and
   gains none in plan 05a. A later facility-layout attribute would reach this context through an ACL that fills
   the same field.
5. The container registry is learned from asrs-control's `ContainerStored` facts (consumer group from the first
   offset) and from this context's own transports. Known limit: a container stored before the topic's retention
   window and never moved since is unknown until it is moved; operators re-register it with a PUTAWAY from the
   clarification point.

## Consequences

- The WES never addresses a robot, a node, a shuttle or a rack position; this context enforces that vocabulary
  rule on its contract (ADR 0005) and its events (ADR 0003).
- Changing a station's buffer is a configuration change of this service (ConfigMap + restart), not a building
  change.
