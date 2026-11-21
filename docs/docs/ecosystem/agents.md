---
id: agents
title: "The read-only agent surfaces"
sidebar_label: "Agents (MCP and A2A)"
description: "The read-only MCP server and A2A agent: five L1 tools and five L1 skills, the resource and the prompt, the endpoints and the Kong paths."
---

# The read-only agent surfaces

`cmd/mcp` and `cmd/a2a` ([ADR 0007](/docs/adr/0007-observability-and-the-read-only-agentic-surfaces)) let an agent
(warehouse-ops-agent, warehouse-console) explain why a transport waits, which leg and which robot, without being able
to move anything. Both are **READ-ONLY at authority L1**: they wire the layout and the Postgres read repositories
into `usecases.Reader` (the same queries the REST adapter serves) and nothing else; they never dial Kafka, never call
a subsystem and never start the relay or the dispatcher. Their pool is read-only at the session level
(`default_transaction_read_only=on`, ruling P4-R17). No authentication of any kind (fleet decision): the ClusterIP
boundary is the control. Equipment ids (robot keys, storage job and leg ids) appear ONLY here, never on the
relationship-1 interface or the published events.

## MCP (`cmd/mcp`, :8090)

Streamable HTTP only, stateless, mounted at `/mcp` and `/`; `GET /healthz` for the probes. Every tool is annotated
`readOnlyHint`, `idempotentHint`, closed-world, and declares authority L1 in its `_meta`.

| Tool | Input | Answers |
| --- | --- | --- |
| `get_transport` | `transportRequestId` | status, reason, `subsystem_reason`, the container's last confirmed location, retries and every leg (seq, attempt, reference, subsystem, kind, from, to, status, the subsystem's job or leg id, the robot) |
| `get_container_track` | `containerId` | position (with the robot when `ON_ROBOT`), active transport, clarification reason, move count |
| `get_station` | `stationId` | buffer limit, occupants in position order, waiting containers in admission order |
| `list_subsystems` | none | every capacity snapshot (status, units, open work, capacity per hour, queue depth) |
| `list_equipment_units` | `asrsId` | the last reported state of every unit of that AS/RS (`IDLE`, `EXECUTE`, `HELD`) |

| Other surface | Name |
| --- | --- |
| Resource template | `station`: `warehouse://material-flow-control/stations/{stationId}` (JSON of one station queue) |
| Prompt | `review_transport` (argument `transportRequestId`): read the transport, its container and its station, and report the waiting leg and its cause |

A failed read is an `isError` tool result `<code>: <detail>`, with the REST adapter's codes:
`transport-request-not-found`, `container-track-not-found`, `station-not-found`, `asrs-not-found`,
`invalid-parameter`; any other error is logged and answered `internal-error` with no infrastructure detail.

## A2A (`cmd/a2a`, :8093)

JSON-RPC 2.0 over HTTP, mounted at `/` and `/a2a`; the Agent Card at `/.well-known/agent-card.json` (no security
scheme, version `SERVICE_VERSION`, interface URL `A2A_AGENT_URL`); `GET /healthz`. Every skill is tagged
`authority:L1`. A call's first message part is a data part (or a text part holding the JSON)
`{"skill": "<id>", "arguments": {...}}`; the answer is one envelope with exactly one of `data` or `problem`, plus a
one-line text summary, and carries the caller's `contextId` only (no task id).

| Skill | Arguments | Mirrors |
| --- | --- | --- |
| `transport_status` | `transportRequestId` | `get_transport` |
| `container_position` | `containerId` | `get_container_track` |
| `station_queue` | `stationId` | `get_station` |
| `subsystem_capacity` | none | `list_subsystems` |
| `equipment_units` | `asrsId` | `list_equipment_units` |

A `problem` is RFC 9457-shaped (`type`, `title`, `status`, `code`, `detail`) and uses the REST adapter's type URI and
code wherever the outcome is shared. Four codes exist only on the agent surfaces: `asrs-not-found` (MCP and A2A),
`unknown-skill`, `malformed-skill-request` and `invalid-argument` (A2A); they are listed next to the REST catalogue on
[Replay classes](/docs/development/replay-classes#codes-only-the-agent-surfaces-answer).

## Spans

Every tool and skill call is one span: `mcp.tool <name>` or `a2a.skill <id>` (`a2a.skill unknown` for an id the card
does not offer), with `gen_ai.operation.name=execute_tool`, `gen_ai.tool.name` and `warehouse.authority=L1`.

## Kong paths

| Kong path | Backend |
| --- | --- |
| `/api/mcp/material-flow-control` | the mcp Service, rewritten to `/mcp` |
| `/api/a2a/material-flow-control` | the a2a Service, rewritten to `/` |
