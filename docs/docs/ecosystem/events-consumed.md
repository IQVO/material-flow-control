---
id: events-consumed
title: "Events consumed"
sidebar_label: "Events consumed"
description: "The ten subsystem facts material-flow-control consumes, its two consumer groups, the DLQs and the pinned producer contracts."
---

# Events consumed

Two fixed-group at-least-once consumers in `cmd/api` ([ADR 0004](/docs/adr/0004-consuming-the-subsystem-facts)):

| Topic | Consumer group (env, default) | DLQ |
| --- | --- | --- |
| `warehouse.asrs-control.events` | `MFC_ASRS_CONSUMER_GROUP`, `material-flow-control.asrs-control-events` | `warehouse.asrs-control.events.dlq` |
| `warehouse.amr-fleet-control.events` | `MFC_AMR_CONSUMER_GROUP`, `material-flow-control.amr-fleet-control-events` | `warehouse.amr-fleet-control.events.dlq` |

`FetchMessage`, then `CommitMessages` only after the handler returned nil; `StartOffset` FirstOffset (a new group
learns every `ContainerStored` within the topic's retention); `CommitInterval` and `MaxWait` unset. The inbox claim
(`processed_events`, keyed by the CloudEvents `id`) and every effect commit in ONE unit of work under the flow lock,
so a redelivery is a no-op. A transient failure retries the same message (200 ms to 5 s, 5 attempts) and then
dead-letters it; a message that is not a CloudEvent, or a consumed type with a malformed payload or a missing
required field, is dead-lettered at once. Every other type on those topics (`RobotStateChanged`, `DeadlockDetected`,
future types) is committed past without effect. The `.analytics` twins are never consumed.

## From asrs-control (relationship 5)

| Type | Required fields | Optional | Effect |
| --- | --- | --- | --- |
| `com.warehouse.wes.asrs-control.storagejob.ContainerStored` | `storage_job_id`, `asrs_id`, `container_id`, `port_id`, `stored_at` | `reference` | the track goes `IN_ASRS` at the AS/RS location (with or without a reference this context owns); a STORE leg ends DONE |
| `com.warehouse.wes.asrs-control.storagejob.ContainerPresentedAtPort` | `storage_job_id`, `asrs_id`, `container_id`, `port_id`, `scan_result` (`READ`, `NO_READ`, `MISMATCH`), `presented_at` | `reference`, `scanned_container_id` | a RETRIEVE ends at the port; `READ` starts the MOVE (or queues it at the station); `NO_READ`/`MISMATCH` sends the container to clarification |
| `com.warehouse.wes.asrs-control.storagejob.StorageJobFailed` | `storage_job_id`, `asrs_id`, `job_kind` (`STORE`, `RETRIEVE`), `container_id`, `port_id`, `reason`, `failed_at` | `reference`, `plc_reason` | re-plan a STORE once, clarification, or `SUBSYSTEM_FAULT` ([Transport lifecycle](/docs/domain/transport-lifecycle#retries-and-re-plans-decision-d13)) |
| `com.warehouse.wes.asrs-control.equipmentunit.EquipmentStateChanged` | `asrs_id`, `unit_id`, `aisle`, `state` and `previous_state` (the pinned enum), `changed_at` | | the unit's last state (`equipment_units`, read by the agents) |
| `com.warehouse.wes.asrs-control.asrs.AsrsCapacityChanged` | `asrs_id`, `units_total`, `units_available`, `positions_total`, `positions_free`, `open_jobs`, `measured_at` | | the AS/RS capacity snapshot ([Capacity](/docs/domain/capacity)) |

## From amr-fleet-control (relationship 4)

| Type | Required fields | Optional | Effect |
| --- | --- | --- | --- |
| `com.warehouse.wes.amr-fleet-control.mission.LegAssigned` | `leg_id`, `fleet_id`, `container_id`, `from_handoff_point_id`, `to_handoff_point_id`, `robot` (`manufacturer`, `serial_number`), `map_version` (a non-empty string, ruling P4-R9), `assigned_at` | `reference` | the robot of the MOVE |
| `com.warehouse.wes.amr-fleet-control.mission.ContainerPickedUp` | `leg_id`, `fleet_id`, `container_id`, `from_handoff_point_id`, `robot`, `picked_up_at` | `reference` | the container is on the robot; at an AS/RS output port a `CONFIRM_PICKUP` is queued; at a station the slot is freed |
| `com.warehouse.wes.amr-fleet-control.mission.LegCompleted` | `leg_id`, `fleet_id`, `container_id`, `to_handoff_point_id`, `robot`, `completed_at` | `reference` | the MOVE ends DONE at its destination |
| `com.warehouse.wes.amr-fleet-control.mission.LegFailed` | `leg_id`, `fleet_id`, `container_id`, `from_handoff_point_id`, `to_handoff_point_id`, `reason` (`CANCELLED`, `ROBOT_BLOCKED`, `ROBOT_FAULT`, `ROBOT_OFFLINE`, `ORDER_REJECTED`, `PICK_FAILED`, `DROP_FAILED`, `MAP_VERSION_RETIRED`), `container_on_robot`, `failed_at` | `reference`, `robot`, `robot_error` | retry, clarification or failure (decision D13) |
| `com.warehouse.wes.amr-fleet-control.fleet.FleetCapacityChanged` | `fleet_id`, `robots_total`, `robots_online`, `robots_available`, `robots_charging`, `robots_held`, `legs_queued`, `legs_active`, `changed_at` | | the fleet capacity snapshot |

Required counts decode as present integers (a `null` or missing count is poison); a negative count clamps to 0. Every
fact is matched by `reference`, this context's leg reference, never by the subsystem's id; a fact without a known
reference still moves the container's track where it applies (`ContainerStored`).

## Pinned contracts

`contracts/pinned.json` lists every producer contract this repo pins; CI's `pinned-contracts` job
(`scripts/harness/pinned_contract_gate.py`) fails when a copy differs from the producer's `develop`, and every pinned
AsyncAPI example of a consumed type decodes through the production handler.

| Copy | Producer | Pinned commit |
| --- | --- | --- |
| `contracts/asrs-control.asyncapi.yaml` | `IQVO/asrs-control` `apis/asyncapi.yaml` | `b23a5a2624e1582101f0e62835547c4806bcfac8` |
| `contracts/asrs-control.openapi.yaml` | `IQVO/asrs-control` `apis/openapi.yaml` | `b23a5a2624e1582101f0e62835547c4806bcfac8` |

amr-fleet-control's two contracts are pinned once its plan merged; until then its fixtures
(`internal/adapters/inbound/kafka/testdata/amr-fleet-control.examples.json`) follow its interface table.
