# ADR 0006: Subsystem capacity — what is measured, how each subsystem maps, and when it is published

## Status

Accepted

## Context

wes-work-planning needs "AMRs available, AS/RS retrieval rate, station queue depth" (relationship 2), and the
handoff asks how capacity is measured and how often. The two subsystem facts do not share a shape:
amr-fleet-control's `FleetCapacityChanged` (`fleet_id`, `robots_total` which may be 0, `robots_online`,
`robots_available` = idle and assignable and at or above the charge threshold, `robots_charging`, `robots_held`,
`legs_queued`, `legs_active`, `changed_at`) and asrs-control's `AsrsCapacityChanged` (`asrs_id`, `units_total` at
least 1, `units_available` = not HELD, `positions_total`, `positions_free`, `open_jobs`, `measured_at`).

## Decision

One `SubsystemCapacityChanged` per subsystem and station, with this explicit mapping (copied, never recomputed):

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
periodically. Facts of one subject arrive in partition order (key = subject), so no reordering by the subsystem's
time is needed; the subsystem's time is kept in the decoded fact for logs only.

## Consequences

- "Within one capacity interval" (the acceptance row) means within the consumer lag of the subsystem fact.
- `units_available` is comparable across kinds only as "can take new work now"; a consumer that needs AS/RS
  storage headroom reads asrs-control directly (positions are not republished).
- wes-work-planning's ADR (plan 05b) decides which fields bound goods-to-person release.
