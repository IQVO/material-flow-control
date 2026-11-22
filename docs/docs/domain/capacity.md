---
id: capacity
title: "Subsystem capacity"
sidebar_label: "Capacity"
description: "How the capacity of the AMR fleet, the AS/RS and the stations is measured, mapped and published as SubsystemCapacityChanged."
---

# Subsystem capacity

One `SubsystemCapacityChanged` per subsystem and station ([ADR 0006](/docs/adr/0006-subsystem-capacity-measurement-and-publication)),
with this explicit mapping, copied and never recomputed:

| Published field | AMR_FLEET (FleetCapacityChanged) | ASRS (AsrsCapacityChanged) | STATION (its queue) |
| --- | --- | --- | --- |
| `subsystem_id` (subject `<kind>/<id>`) | `fleet_id` | `asrs_id` | station id |
| `units_total` | `robots_total` (0 accepted) | `units_total` (0 accepted, never sent) | `buffer_limit` |
| `units_available` = idle-and-dispatchable | `robots_available` (a robot runs one leg at a time) | `units_available` (an aisle unit queues jobs, so a not-HELD unit is dispatchable) | `buffer_limit - occupied` (min 0) |
| `open_work` = queued + active | `legs_queued + legs_active` | `open_jobs` (queued and executing) | `occupied + waiting` |
| `status` UNAVAILABLE | `robots_online` = 0 (includes `robots_total` 0) | `units_available` = 0 | never |
| `status` DEGRADED | available < total | available < total | arrivals wait upstream |
| `capacity_per_hour` | available x `legsPerRobotHour` (30) | available x `retrievalsPerUnitHour` (60) | 0 |
| `buffer_limit`, `occupied`, `queue_depth` | 0 | 0 | the queue's |
| not published | `robots_online` (status only), `robots_charging`, `robots_held` | `positions_total`, `positions_free` | — |
| `measured_at` | this context's clock when the fact was applied; `changed_at` is never copied | this context's clock; asrs-control's `measured_at` is never copied | this context's clock |

Negative counts clamp to 0. A snapshot is published when any field other than `measured_at` changed, never
periodically, so it follows a subsystem fact within the consumer lag. `buffer_limit`, `occupied` and `queue_depth`
are meaningful only for `STATION` (0 elsewhere). Never sum `units_available` or `open_work` across kinds: an AMR
`units_available` is idle and assignable robots, an AS/RS `units_available` is units that are not `HELD` and may be
busy. `GET /subsystems` lists the latest snapshots sorted by kind, then id.

The rejection `SUBSYSTEM_UNAVAILABLE` reads these snapshots: a subsystem on the route whose latest snapshot is
`UNAVAILABLE` rejects the request.
