---
id: events-published
title: "Events published"
sidebar_label: "Events published"
description: "The seven CloudEvents types material-flow-control publishes, their subjects and every payload field."
---

# Events published

Seven CloudEvents 1.0 types ([ADR 0003](/docs/adr/0003-cloudevents-outbox-and-the-seven-facts)), structured mode,
`source` `/warehouse/material-flow-control`, `dataschema` `urn:warehouse:material-flow-control:<events|analytics>:<EventName>:v1`.
Every event is written to BOTH `warehouse.material-flow-control.events` and `warehouse.material-flow-control.analytics`
under the same `id` (the transactional outbox); the Kafka key is the `subject`; `data` is snake_case; every `*_at` and
the CloudEvents `time` are this context's own clock. No payload names a robot, leg, node, shuttle, aisle, rack
position, port or storage job (a golden test fails a field that does). The contract is `apis/asyncapi.yaml`.

| Consumer (planned) | Reads |
| --- | --- |
| fulfillment-execution | `TransportAccepted`, `TransportRejected`, `ContainerArrivedAtStation`, `TransportCompleted`, `TransportFailed` |
| wes-work-planning | `SubsystemCapacityChanged` |
| exception-management | `ContainerSentToClarification` |

A consumer dispatches on the full `type` and dedupes on `id`. A PICK publishes `ContainerArrivedAtStation` and then
`TransportCompleted`; every accepted request ends in exactly one of `TransportCompleted` or `TransportFailed`.

## The seven types

### TransportRejected

`com.warehouse.wes.material-flow-control.transportrequest.TransportRejected`; subject and Kafka key: transport_request_id.

| Field | Type | Required | Meaning |
| --- | --- | --- | --- |
| `transport_request_id` | string | yes |  |
| `container_id` | string | yes |  |
| `source` | string | yes | A LocationCode |
| `destination` | string | yes |  |
| `purpose` | string | yes | One of `PICK`, `PUTAWAY`, `REPLENISH`, `OUTBOUND`, `RETURN`. |
| `reason` | string | yes | One of `UNKNOWN_CONTAINER`, `CONTAINER_BUSY`, `DESTINATION_UNREACHABLE`, `SUBSYSTEM_UNAVAILABLE`. |
| `detail` | string | yes |  |
| `rejected_at` | string | yes |  |

### TransportAccepted

`com.warehouse.wes.material-flow-control.transportorder.TransportAccepted`; subject and Kafka key: the transport order id.

| Field | Type | Required | Meaning |
| --- | --- | --- | --- |
| `transport_order_id` | string | yes |  |
| `transport_request_id` | string | yes |  |
| `container_id` | string | yes |  |
| `source` | string | yes | A LocationCode |
| `destination` | string | yes |  |
| `purpose` | string | yes | One of `PICK`, `PUTAWAY`, `REPLENISH`, `OUTBOUND`, `RETURN`. |
| `priority` | string | yes | One of `NORMAL`, `URGENT`. |
| `due_by` | string | yes |  |
| `legs_planned` | integer | yes |  |
| `accepted_at` | string | yes |  |

### ContainerArrivedAtStation

`com.warehouse.wes.material-flow-control.transportorder.ContainerArrivedAtStation`; subject and Kafka key: the transport order id.

| Field | Type | Required | Meaning |
| --- | --- | --- | --- |
| `transport_order_id` | string | yes |  |
| `transport_request_id` | string | yes |  |
| `container_id` | string | yes |  |
| `station_id` | string | yes |  |
| `purpose` | string | yes | One of `PICK`, `PUTAWAY`, `REPLENISH`, `OUTBOUND`, `RETURN`. |
| `priority` | string | yes | One of `NORMAL`, `URGENT`. |
| `due_by` | string | yes |  |
| `queue_position` | integer | yes |  |
| `arrived_at` | string | yes |  |

### TransportCompleted

`com.warehouse.wes.material-flow-control.transportorder.TransportCompleted`; subject and Kafka key: the transport order id.

| Field | Type | Required | Meaning |
| --- | --- | --- | --- |
| `transport_order_id` | string | yes |  |
| `transport_request_id` | string | yes |  |
| `container_id` | string | yes |  |
| `destination` | string | yes |  |
| `purpose` | string | yes | One of `PICK`, `PUTAWAY`, `REPLENISH`, `OUTBOUND`, `RETURN`. |
| `completed_at` | string | yes |  |

### TransportFailed

`com.warehouse.wes.material-flow-control.transportorder.TransportFailed`; subject and Kafka key: the transport order id.

| Field | Type | Required | Meaning |
| --- | --- | --- | --- |
| `transport_order_id` | string | yes |  |
| `transport_request_id` | string | yes |  |
| `container_id` | string | yes |  |
| `purpose` | string | yes | One of `PICK`, `PUTAWAY`, `REPLENISH`, `OUTBOUND`, `RETURN`. |
| `reason` | string | yes | One of `CLARIFICATION`, `SUBSYSTEM_FAULT`, `CANCELLED`, `TIMEOUT`. |
| `subsystem_reason` | string | no | The subsystem's bare reason code (for example ROBOT_BLOCKED) |
| `container_location` | string | no | The container's last confirmed location (a location code or station id) |
| `failed_at` | string | yes |  |

### ContainerSentToClarification

`com.warehouse.wes.material-flow-control.containertrack.ContainerSentToClarification`; subject and Kafka key: the container id.

| Field | Type | Required | Meaning |
| --- | --- | --- | --- |
| `container_id` | string | yes |  |
| `transport_order_id` | string | no | The transport order whose leg found the problem; may name an internal clarification move, and then transport_request_id is absent |
| `transport_request_id` | string | no | The WES request of that transport; absent for an internal clarification move |
| `reason` | string | yes | One of `NO_READ`, `MISMATCH`, `CONTAINER_NOT_AT_SOURCE`, `CONTAINER_NOT_AT_PORT`, `STORAGE_MAP_MISMATCH`, `CONTAINER_ON_FAILED_ROBOT`. |
| `scanned_container_id` | string | no |  |
| `detected_at_location` | string | yes | The location code or station id where the problem was detected; for a container on a failed robot, the place the failed leg started from |
| `clarification_point` | string | no | The clarification point's location code |
| `sent_at` | string | yes |  |

### SubsystemCapacityChanged

`com.warehouse.wes.material-flow-control.subsystemcapacity.SubsystemCapacityChanged`; subject and Kafka key: `<kind>/<id>` (e.g. `AMR_FLEET/AMR1`, `ASRS/ASRS1`, `STATION/ST1`).

| Field | Type | Required | Meaning |
| --- | --- | --- | --- |
| `subsystem_kind` | string | yes | One of `AMR_FLEET`, `ASRS`, `STATION`. |
| `subsystem_id` | string | yes |  |
| `status` | string | yes | One of `AVAILABLE`, `DEGRADED`, `UNAVAILABLE`. |
| `units_total` | integer | yes |  |
| `units_available` | integer | yes |  |
| `open_work` | integer | yes |  |
| `capacity_per_hour` | integer | yes |  |
| `buffer_limit` | integer | yes |  |
| `occupied` | integer | yes |  |
| `queue_depth` | integer | yes |  |
| `measured_at` | string | yes |  |
