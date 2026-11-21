# ADR 0007: Observability and the read-only agentic surfaces

## Status

Accepted

## Context

The fleet standard (2026-11-20) requires every repo to ship OpenTelemetry, a read-only MCP server and an A2A agent
from its first release. The context map leaves read-only consumers undrawn: warehouse-ops-agent and
warehouse-console read container positions and equipment state.

## Decision

1. Tier 1 OpenTelemetry in all three binaries (OTLP/gRPC traces and metrics, W3C propagation on REST in and out and
   on Kafka in and out, runtime metrics, otelchi spans and the request-duration histogram, trace ids on every log
   line); an empty `OTEL_EXPORTER_OTLP_ENDPOINT` turns it off.
2. One Tier 2 counter, `material_flow_control.transports.ended` (`purpose`, `outcome`, `reason`), recorded after
   commit, so a replay or a redelivery never counts twice.
3. `cmd/mcp` (:8090, Streamable HTTP, stateless) and `cmd/a2a` (:8093) are READ-ONLY at authority L1: five tools /
   skills over a `Reader` that has no write method; they never dial Kafka, never call a subsystem, never start
   the relay or the dispatcher (a fitness test enforces the imports).
4. Equipment ids (robot keys, storage job and leg ids) appear ONLY in these read-only views, never on the
   relationship-1 REST interface or the published events.

## Consequences

- Dashboards key on the counter name; renaming it needs a new ADR.
- An agent can explain why a transport waits (which leg, which robot) without being able to move anything.
