---
id: troubleshooting
title: "Troubleshooting"
sidebar_label: "Troubleshooting"
description: "Symptoms, causes and fixes: boot errors, rejections, stuck transports, dead letters, replays and capacity that does not move."
---

# Troubleshooting

| Symptom | Likely cause | What to do |
| --- | --- | --- |
| The pod exits at once with one JSON line `material-flow-control exited with error` | a missing or invalid variable (the line names every one), a busy `ADDR`, a layout file that does not parse | Fix the variable or the layout ([Configuration](/docs/operations/configuration)). |
| `/readyz` answers `503` `dependency-unavailable` | Postgres or Kafka does not answer (the detail names which) | Check the dependency; the pod stays out of the Service meanwhile. |
| Every request for a stored tote is rejected `UNKNOWN_CONTAINER` | this context never saw its `ContainerStored` (stored before the topic's retention, or the asrs consumer is behind) | Check the consumer group lag of `MFC_ASRS_CONSUMER_GROUP`; a container stored before the retention window is re-registered with a PUTAWAY from the clarification point ([ADR 0001](/docs/adr/0001-material-flow-control-bounded-context)). |
| A request is rejected `CONTAINER_BUSY` | the container has an active transport, or is held at clarification | `GET /container-tracks/{containerId}`: `active_transport_request_id` or `clarification`. |
| A request is rejected `SUBSYSTEM_UNAVAILABLE` | the latest capacity snapshot of a subsystem on the route is `UNAVAILABLE` | `GET /subsystems`; the AMR fleet is `UNAVAILABLE` with no robot online, the AS/RS with no unit that is not `HELD`. |
| `400 invalid-parameter` for a well-formed request | the purpose rule (decision D4) or source = destination | `PICK` needs a station destination; `PUTAWAY`/`RETURN` the AS/RS as destination; `REPLENISH` the AS/RS as source. |
| A transport stays `IN_PROGRESS` | a leg waits for a station slot, a command is being retried, or a subsystem has not reported | MCP `get_transport` or A2A `transport_status` names the waiting leg and its robot; see [A stuck subsystem command](/docs/operations/runbook#a-stuck-subsystem-command). |
| A transport fails `SUBSYSTEM_FAULT` with `subsystem_reason` `unknown-handoff-point` | the layout names a handoff point amr-fleet-control's map does not have | Add the node to the map, or fix the layout. |
| A transport fails `TIMEOUT` | no progress fact for `LEG_TIMEOUT_SECONDS` | [LEG_TIMEOUT failures](/docs/operations/runbook#leg_timeout-failures). |
| The same POST answers `409 idempotency-key-reused` | the key was used with another method, path or body | Send a new key per logical request. |
| A retried POST runs again instead of replaying | the first answer was RETRYABLE (key released) | Expected: a RETRYABLE answer is never stored, so a retry executes again. |
| Messages in `warehouse.asrs-control.events.dlq` or `warehouse.amr-fleet-control.events.dlq` | a malformed fact, or a handler that failed 5 times | [Dead-letter topics](/docs/operations/runbook#dead-letter-topics). |
| `material_flow_control.outbox.lag` grows | Kafka does not take the relay's writes | Check the brokers. A row that failed `OUTBOX_MAX_ATTEMPTS` times is excluded from future claims (ERROR `outbox relay: row reached the attempt cap ...`) and stays in `outbox_events`; after fixing the cause, re-arm it with `UPDATE outbox_events SET attempts = 0 WHERE id = <id>`. |
| `SubsystemCapacityChanged` never changes | the subsystem publishes no capacity fact, or nothing but `measured_at` changed (published on change only) | Check the producer's `AsrsCapacityChanged` / `FleetCapacityChanged`. |
