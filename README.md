# material-flow-control

material-flow-control is the WCS material flow controller of the warehouse-systems fleet and the one door from the
WES to the automation. It accepts or rejects a WES transport request at once, moves the container through handoff
points across `asrs-control` and `amr-fleet-control`, keeps the one physical position of every container inside the
automation, and publishes its facts: transport requests accepted or rejected, containers arriving at goods-to-person
stations, transports completed or failed, containers sent to clarification, and the aggregated capacity of the AMR
fleet, the AS/RS and the stations. CloudEvents 1.0 structured mode; every event is written to
`warehouse.material-flow-control.events` and `warehouse.material-flow-control.analytics` under the same id; the Kafka
key is the subject; every `*_at` is material-flow-control's own clock. No payload names a robot, leg, node, shuttle,
aisle, rack position, port or storage job (seam decision 3). Classification: **Supporting** (ADR 0001).

## Disclaimer

This project is a domain-driven design study. It is not associated with, endorsed by or affiliated with any company,
and it uses no company's code, data, logos or trademarks. All names, figures and examples are illustrative or
synthetic.

## Documentation

The documentation site is published at **https://iqvo.github.io/material-flow-control/** (built from `docs/` by
`.github/workflows/docs.yml`): overview and quickstart, configuration, runbook, observability, deployment, the domain
model, the relationship-1 interface, the events published and consumed, the agent surfaces, the generated REST
reference and the ADRs.

## Architecture

Hexagonal Go service with three binaries:

| Binary | Port | Role |
| --- | --- | --- |
| `cmd/api` | `:8080` | the relationship-1 REST interface, the two subsystem fact consumers, the subsystem command dispatcher, the leg-timeout tick, the outbox relay |
| `cmd/mcp` | `:8090` | read-only MCP server (Streamable HTTP, stateless): `get_transport`, `get_container_track`, `get_station`, `list_subsystems`, `list_equipment_units` (authority L1) |
| `cmd/a2a` | `:8093` | read-only A2A agent (JSON-RPC 2.0): `transport_status`, `container_position`, `station_queue`, `subsystem_capacity`, `equipment_units` (authority L1) |

Four aggregates (`TransportRequest`, `TransportOrder`, `ContainerTrack`, `StationQueue`) and the pure `layout`,
`routing` and `capacity` packages are driven by one application `Service`. Every write runs in one Postgres unit of
work that first takes the flow lock (`SELECT ... FOR UPDATE` on `flow_lock`), so writes from REST, Kafka facts, the
dispatcher and the tick are serial. No subsystem call runs inside a transaction: a use case queues a
`subsystem_commands` row and the dispatcher calls the subsystem with the command's `Idempotency-Key` after commit.
Postgres tables: `flow_lock`, `transport_requests`, `transport_orders`, `transport_legs`, `container_tracks`,
`container_moves`, `station_queues`, `subsystem_capacities`, `equipment_units`, `subsystem_commands`,
`processed_events`, `outbox_events`, `idempotency_keys`.

## Domain events / Integration

Published (`com.warehouse.wes.material-flow-control.<entity>.<Event>`): `TransportRejected`, `TransportAccepted`,
`ContainerArrivedAtStation`, `TransportCompleted`, `TransportFailed`, `ContainerSentToClarification`,
`SubsystemCapacityChanged`.

Consumed, from `warehouse.asrs-control.events` (group `MFC_ASRS_CONSUMER_GROUP`): `ContainerStored`,
`ContainerPresentedAtPort`, `StorageJobFailed`, `EquipmentStateChanged`, `AsrsCapacityChanged`; from
`warehouse.amr-fleet-control.events` (group `MFC_AMR_CONSUMER_GROUP`): `LegAssigned`, `ContainerPickedUp`,
`LegCompleted`, `LegFailed`, `FleetCapacityChanged`. Every other type (`RobotStateChanged`, ...) is committed past.
Malformed and exhausted messages go to `<topic>.dlq`. The consumed producer contracts are pinned under `contracts/`.

## Running it

`cmd/api` runs on real Postgres, Kafka and the two subsystem controllers only. Mandatory: `DATABASE_URL`,
`KAFKA_BROKERS`, `ASRS_CONTROL_URL`, `AMR_FLEET_CONTROL_URL`, `MFC_LAYOUT_FILE` (the material-flow layout, e.g.
`config/flow-layout.example.yaml`). Optional: `MIGRATIONS_DATABASE_URL`, `ADDR`, `LOG_LEVEL`,
`MFC_ASRS_CONSUMER_GROUP`, `MFC_AMR_CONSUMER_GROUP`, `OUTBOX_INTERVAL_MS`, `OUTBOX_BATCH`, `OUTBOX_MAX_ATTEMPTS`,
`SUBSYSTEM_TIMEOUT_MS`, `DISPATCH_POLL_MS`, `TICK_INTERVAL_MS`, `LEG_RETRY_LIMIT`, `LEG_REQUEST_DEADLINE_SECONDS`,
`LEG_TIMEOUT_SECONDS`, `OTEL_SERVICE_NAME`, `SERVICE_VERSION`, `OTEL_EXPORTER_OTLP_ENDPOINT`, `ENVIRONMENT`.
`cmd/mcp` needs `DATABASE_URL` and `MFC_LAYOUT_FILE` (`MCP_ADDR`); `cmd/a2a` also `A2A_AGENT_URL` (`A2A_ADDR`). A
missing or invalid variable is a boot error: one JSON line naming it, exit code 1. Defaults and meanings:
`docs/docs/operations/configuration.md`.

```bash
MFC_LAYOUT_FILE=config/flow-layout.example.yaml go run ./cmd/api
```

## Observability

OpenTelemetry Tier 1 in all three binaries (OTLP/gRPC traces and metrics, W3C propagation on REST and Kafka, runtime
metrics, the request-duration histogram, `trace_id`/`span_id` on every JSON log line); an empty
`OTEL_EXPORTER_OTLP_ENDPOINT` turns it off. `cmd/api` adds the counter `material_flow_control.transports.ended`
(`purpose`, `outcome`, `reason`) and the gauges `material_flow_control.outbox.backlog` and
`material_flow_control.outbox.lag` (ADR 0007).

## API

The relationship-1 interface (`apis/openapi.yaml`; through Kong at `/api/material-flow-control`). Every POST
requires `Idempotency-Key`; errors are `application/problem+json` with a `code`; no authentication.

| Method and path | Success | Refusals |
| --- | --- | --- |
| `POST /transport-requests` | `202` `TransportOrder` | `400` `bad-request` / `invalid-parameter`, `409` `transport-request-conflict`, `422` `unknown-container` / `container-busy` / `destination-unreachable` / `subsystem-unavailable` |
| `GET /transport-requests/{transportRequestId}` | `200` `TransportOrder` | `400` `invalid-parameter`, `404` `transport-request-not-found` |
| `POST /transport-requests/{transportRequestId}/cancellation` | `200` cancelled, `202` asked of the subsystem | `404` `transport-request-not-found`, `409` `transport-not-cancellable` / `transport-ended` |
| `GET /container-tracks/{containerId}` | `200` `ContainerTrack` | `400` `invalid-parameter`, `404` `container-track-not-found` |
| `POST /container-tracks/{containerId}/clarification-resolution` | `200` `ContainerTrack` | `400` `bad-request`, `404` `container-track-not-found`, `409` `not-in-clarification`, `422` `unknown-location` |
| `GET /stations/{stationId}` | `200` `StationQueue` | `400` `invalid-parameter`, `404` `station-not-found` |
| `GET /subsystems` | `200` the capacity snapshots | |
| `GET /healthz`, `GET /readyz` | `200` | `503` while draining or a dependency is down |

## Helm chart

`charts/material-flow-control` (added by the packaging step, not in the repository yet): one api Deployment (exactly
one replica, `Recreate`), its Service, a ConfigMap with the environment and `flow-layout.yaml`, a Secret for the
database URLs; default-off read-only mcp and a2a Deployments, the Kong routes `/api/material-flow-control`,
`/api/mcp/material-flow-control` and `/api/a2a/material-flow-control`, and the KongPlugins `cors`, `prometheus` and
`opentelemetry`.

## Local development

```bash
make check-fast                       # format, vet, fitness tests, changed packages
make check                            # fmt-check, vet, build, lint, test (pre-push)
cd docs && npm ci && npm run build    # the docs site (Node 20)
```

GitFlow: feature branches into `develop` through pull requests; `main` is the release branch.

## Tests

`go test ./... -race` (unit), `make bdd` (the eight acceptance scenarios, godog), `make integration`
(`//go:build integration`, Postgres and Kafka in testcontainers), `make arch-test` (hexagonal rule and fitness
tests), `make coverage` (90% gate over domain and application), `make mutation` (gremlins).

## Architecture Decision Records

`docs/adr/`: 0001 the bounded context (Supporting; station buffers are this context's configuration), 0002 the
layout, routing and the command outbox, 0003 CloudEvents and the seven facts, 0004 consuming the subsystem facts,
0005 the relationship-1 REST interface and its replay classes, 0006 subsystem capacity, 0007 observability and the
read-only agentic surfaces.

## License

MIT — see [LICENSE](LICENSE).
