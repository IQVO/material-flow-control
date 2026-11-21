# ADR 0003: CloudEvents through the outbox, and the seven facts material-flow-control publishes

## Status

Accepted

## Context

The WES drives every transport from facts, never by polling (seam decision 3). The facts must survive a crash
between the state change and the publish, and must not carry equipment vocabulary to the WES.

## Decision

1. Every domain event is encoded to CloudEvents 1.0 structured mode in the unit of work that raised it and written
   to the transactional outbox on BOTH `warehouse.material-flow-control.events` and `.analytics` under one id; the
   relay (copied from asrs-control) publishes it. Kafka key = subject.
2. The seven types, entity segment = the raising aggregate (plan Q2, deviating from seam decision 5's example
   segment `transport`, which names no aggregate):
   `transportrequest.TransportRejected` (subject = transport_request_id), `transportorder.TransportAccepted`,
   `transportorder.ContainerArrivedAtStation`, `transportorder.TransportCompleted`, `transportorder.TransportFailed`
   (subject = transport order id), `containertrack.ContainerSentToClarification` (subject = container id),
   `subsystemcapacity.SubsystemCapacityChanged` (subject = `<kind>/<id>`).
3. Every `*_at` and the CloudEvents `time` are this context's clock; a subsystem's own time is never copied.
4. No payload names a robot, leg, node, shuttle, aisle, rack position, port or storage job; a golden test fails a
   field that does.
5. An event without an encoder fails the unit of work (never a silent drop).

## Consequences

- A PICK publishes `ContainerArrivedAtStation` and then `TransportCompleted`: every accepted request ends in
  exactly one outcome, and the arrival is the WES's cue to start the pick.
- warehouse-docs owes an edit of the example type in seam decision 5 and the context page (plan Q2).

## Type catalogue

The full CloudEvents `type` of every fact this context publishes (`apis/asyncapi.yaml`; source
`/warehouse/material-flow-control`, dataschema `urn:warehouse:material-flow-control:<events|analytics>:<EventName>:v1`):

| Type | Subject and Kafka key |
| --- | --- |
| `com.warehouse.wes.material-flow-control.transportrequest.TransportRejected` | transport_request_id |
| `com.warehouse.wes.material-flow-control.transportorder.TransportAccepted` | transport order id |
| `com.warehouse.wes.material-flow-control.transportorder.ContainerArrivedAtStation` | transport order id |
| `com.warehouse.wes.material-flow-control.transportorder.TransportCompleted` | transport order id |
| `com.warehouse.wes.material-flow-control.transportorder.TransportFailed` | transport order id |
| `com.warehouse.wes.material-flow-control.containertrack.ContainerSentToClarification` | container id |
| `com.warehouse.wes.material-flow-control.subsystemcapacity.SubsystemCapacityChanged` | `<kind>/<id>` |
