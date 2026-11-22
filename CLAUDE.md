# CLAUDE.md — material-flow-control

`material-flow-control` is a bounded context of the `warehouse-systems` fleet (GitHub org `IQVO`, Go module
github.com/IQVO/material-flow-control, scaffolded from warehouse-harness-template).
It is the **material flow controller** of the WCS tier, classification **Supporting**, CloudEvents segment `wes`,
and the ONE door from the WES to the automation (context map relationship 1). It answers "can this container be
moved there now, by which legs, and where is it physically?". It owns transport orders (accept or reject a WES
transport request at once, split it into legs through handoff points, one outcome each), container tracking (the
physical position of every container inside the automation), station sequencing (priority then due time, the
station buffer limit, ADR 0001), clarification of unknown or mismatched containers, and the subsystem capacity view
the WES consumes. It does NOT own what is in a container (inventory-storage), which container moves or when work is
released (fulfillment-execution, wes-work-planning), robot paths and traffic (amr-fleet-control), rack positions
and shuttle scheduling (asrs-control), or the anomaly lifecycle (exception-management). Richest aggregate: package
`transportorder`. Three binaries: `cmd/api` (REST on :8080, the two Kafka consumers, the subsystem command
dispatcher, the outbox relay), `cmd/mcp` (read-only MCP on :8090), `cmd/a2a` (read-only A2A on :8093).

## Hard rules (each one cost an incident or is fitness-tested)

1. **Hexagonal dependency rule.** The domain layer (internal/domain) depends on nothing internal but domain;
   the application layer only on domain and application; inbound and outbound adapters never depend on each
   other; only cmd/ wires layers. No JSON tags, SQL or HTTP types in the domain. `make arch-test` enforces it
   (`internal/architecture/architecture_test.go`).
2. **Real infrastructure at runtime.** A cmd/ binary never imports the in-memory adapters (test doubles only;
   the fleet runs on real Postgres and Kafka): `TestRuntimeBinaryHasNoInMemoryAdapters`. No `.go` file at the
   module root (`TestNoGoFilesAtRepoRoot`); the godog runner lives in features/.
3. **No auth layer on REST or MCP** (fleet-wide revert 2026-09-11): never add bearer/JWT/API-key middleware
   (`TestNoAuthMiddlewareReintroduced`). Agent authority is declared per MCP tool / A2A skill instead
   (`.claude/rules/fleet/agent-authority.md`).
4. **No outbound calls to sibling contexts without an ADR.** Cross-context data arrives as events kept in
   local copies (`.claude/rules/fleet/context-boundaries.md`).
5. **An ADR comes first** for a new cross-context contract or any auth.
   This repo's own rules: (a) no equipment vocabulary (robot, node, shuttle, aisle, rack position, storage job,
   leg) in the relationship-1 REST bodies or the published events; (b) a leg starts only after the previous
   leg's handoff fact, and facts are matched by OUR leg reference, never by a subsystem id (ADR 0002); (c) no REST
   call to a subsystem inside a unit of work: enqueue a `subsystem_commands` row, the dispatcher calls; (d) every
   write unit of work takes the flow lock first; (e) the four transport rejections are replay class OUTCOME;
   (f) Kafka consumers: FetchMessage + commit after handling, inbox claim in the same unit of work, DLQ, groups
   from env; (g) one replica of `cmd/api`; `cmd/mcp` and `cmd/a2a` are read-only (authority L1).
6. **Events: CloudEvents 1.0 is MANDATORY** on every Kafka message produced or consumed (structured mode,
   no flat envelope, no dual-write, no dual-read, no envelope toggle; `TestNoEventEnvelopeToggleOrFlatEnvelope`).
   Build and decode ONLY through `internal/adapters/kafka/cloudevents/` (generated from
   `templates/cloudevents/cloudevents.go.tmpl`); `type` is
   `com.warehouse.wes.material-flow-control.<entity>.<EventName>`, with `source` and
   `dataschema` exactly as that helper builds them. A breaking payload change is a new `.v2` type, never a
   mutation. Consumers dispatch on the FULL `type`, dedupe on `id`, and skip/DLQ (never crash on) anything
   that fails validation. Full rule: `.claude/rules/integration-events.md`.
7. **Events leave through the transactional outbox only**: the aggregate save and the encoded CloudEvents
   commit in one transaction; a relay publishes. Never write to Kafka from a handler or a use case.

## Commands

```bash
make check-fast   # fmt-check + vet + arch-test: run before saying "done"
make check        # fmt-check vet build lint test
make check-all    # check + coverage + arch-test + bdd (the full local gate)
make integration  # real Postgres/Kafka via testcontainers (needs Docker)
make mutation     # gremlins once per domain package, as CI (thresholds in .gremlins.yaml, measured first)
make guide-lint   # these guides: skills load, references resolve, context budget
make harness-test # unit-test the agent hooks
```

`cmd/api`: `DATABASE_URL`, `KAFKA_BROKERS`, `ASRS_CONTROL_URL` (e.g. `http://localhost:8081`),
`AMR_FLEET_CONTROL_URL` (e.g. `http://localhost:8082`) and `MFC_LAYOUT_FILE` (e.g. `config/flow-layout.example.yaml`)
are required; optional: `MIGRATIONS_DATABASE_URL`, `ADDR` (`:8080`), `LOG_LEVEL`, `MFC_ASRS_CONSUMER_GROUP`,
`MFC_AMR_CONSUMER_GROUP`, `OUTBOX_INTERVAL_MS`, `OUTBOX_BATCH`, `OUTBOX_MAX_ATTEMPTS`, `DISPATCH_POLL_MS`,
`TICK_INTERVAL_MS`, `SUBSYSTEM_TIMEOUT_MS`, `LEG_RETRY_LIMIT`, `LEG_REQUEST_DEADLINE_SECONDS`, `LEG_TIMEOUT_SECONDS`,
`ORDER_RETENTION_HOURS`, `CONTAINER_MOVE_RETENTION_HOURS`, `COMMAND_RETENTION_HOURS`, `OUTBOX_RETENTION_HOURS`, `PURGE_BATCH`,
`OTEL_EXPORTER_OTLP_ENDPOINT` (empty = telemetry off), `OTEL_SERVICE_NAME`, `SERVICE_VERSION`,
`ENVIRONMENT` (telemetry's `deployment.environment.name`; the chart sets it).
`cmd/mcp`: `DATABASE_URL`, `MFC_LAYOUT_FILE`; `MCP_ADDR` (`:8090`). `cmd/a2a`: `DATABASE_URL`, `MFC_LAYOUT_FILE`,
`A2A_AGENT_URL`; `A2A_ADDR` (`:8093`). Run asrs-control and amr-fleet-control (and the equipment emulator) beside it.

## Where things are written down (read before editing the matching area)

- `.claude/rules/domain-model.md`: ubiquitous language, aggregates, events, use cases.
  `.claude/rules/rest-api.md`: endpoints and error slugs.
  `.claude/rules/integration-events.md`: published/consumed events and the CloudEvents rule.
- `.claude/rules/fleet/`: fleet-wide rules copied from the harness template; never edit them here
  (their canonical copies live in warehouse-docs, agents/fleet).
- `HARNESS.md`: what each sensor runs and why. `INCIDENTS.md`: the fleet incident log (template-managed,
  byte-identical in every repo; add a row in warehouse-harness-template first).
  `.claude/memory/MEMORY.md`: this repo's own durable heuristics.
- `docs/before-first-release.md`: packaging checklist to finish before the first develop -> main release.
  ADRs are in docs/adr (index.md).

harness-template: v4

<!-- harness:scoped-rules:start (generated by tools/migrate_v3.py in warehouse-harness-template; do not hand-edit) -->
## Scoped rules and harness

Claude Code loads each rule below automatically when you touch the matching paths. OpenCode and Codex do NOT: read the rule BEFORE editing matching files.

| When touching | Read |
|---|---|
| `internal/adapters/**/kafka/**`, `internal/adapters/outbound/events/**`, `apis/asyncapi*` | `.claude/rules/integration-events.md` |
| `internal/adapters/inbound/http/**`, `apis/openapi*.yaml`, `apis/openapi/**` | `.claude/rules/rest-api.md` |

Hooks (`scripts/harness/hook.py`, wired for Claude Code, Codex and OpenCode) block pushes to develop/main, `--no-verify`, bare `rm -rf`, and edits to generated files, and feed gofmt/vet findings back after each edit. Before saying "done" run `make check-fast`; the full gate is `make check-all`. `HARNESS_OFF=1` disables the hooks when debugging the harness itself.
<!-- harness:scoped-rules:end -->
