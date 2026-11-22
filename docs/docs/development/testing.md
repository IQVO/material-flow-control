---
id: testing
title: "Testing"
sidebar_label: "Testing"
description: "Unit tests, the eight BDD acceptance scenarios, integration tests on real Postgres and Kafka, the fitness tests, coverage and mutation gates."
---

# Testing

| Layer | Command | What it proves |
| --- | --- | --- |
| Unit | `go test ./... -race` (part of `make check`) | domain aggregates and policies, use cases over the in-memory doubles (`internal/adapters/outbound/memory`), the adapters' encoding, decoding and problem catalogues, the composition roots' configuration and shutdown order |
| BDD | `make bdd` | the eight acceptance rows of the context's implementation handoff, as godog scenarios over the use cases |
| Integration | `make integration` (`go test -tags integration ./...`) | the Postgres repositories, the flow lock and its timeouts, the Idempotency middleware over real Postgres, the consumers and the relay over real Kafka, `cmd/api` traces and outbox gauges, the end-to-end test against the released subsystems |
| Fitness | `make arch-test` | the hexagonal import rules (arch-go) and the sensors in `internal/architecture` |
| Coverage | `make coverage` | at least 90% of `./internal/domain/...` and `./internal/application/...` |
| Mutation | `make mutation` | gremlins per package (efficacy and mutant coverage), thresholds measured before the first release |

`make check-fast` (format, vet, fitness tests and the tests of the changed packages) is the quick local gate;
`make check` (fmt-check, vet, build, lint, test) is the pre-push gate. Tests assert exact values: statuses, reasons,
positions, counts and queue order, never only "no error".

## BDD: the eight acceptance rows

`features/material_flow.feature`, run by `features/material_flow_test.go` (godog) over the use cases with the memory
doubles:

1. Goods-to-person, then the return writes no stock fact
2. Putaway into the AS/RS
3. Replenishment to the forward handoff, then the return
4. Outbound to a pack station
5. A second request for a busy container is rejected
6. A retried request returns the original outcome
7. A no-read at a port sends the container to clarification
8. Half the robots go offline and the capacity reflects it

Each scenario's description names the cross-context half it defers (the WES side, exception-management) and its
owner.

## Integration tests

Every test that needs a container carries `//go:build integration`, compiles under `go vet -tags integration ./...`,
never calls `t.Skip` and never reads a database URL from the environment. `internal/testing/pgtest` starts one
Postgres 16 container per test binary; `internal/testing/kafkatest` starts `confluentinc/confluent-local:7.6.1` with
topic auto-creation off, rewrites the brokers to `127.0.0.1` and creates one-partition topics with an awaited leader
before a consumer under test is built; both join the test binary's one Docker network (`internal/testing/dockernet`).
CI's `integration` job is their gate.

## The end-to-end test

`cmd/api/e2e_subsystems_integration_test.go` (`TestTheEightAcceptanceRowsAgainstTheRealSubsystems`) runs
material-flow-control in-process against the REAL subsystems, each the image built from its released commit, with no
in-test subsystem fake:

| Subsystem | Release | Commit | Environment variable |
| --- | --- | --- | --- |
| `IQVO/equipment-emulator` | v0.1.0 | `b58201e` | `EQUIPMENT_EMULATOR_IMAGE` |
| `IQVO/asrs-control` | v0.1.1 | `a3e172d` (its contracts equal the pinned `b23a5a2`) | `ASRS_CONTROL_IMAGE` |
| `IQVO/amr-fleet-control` | v0.1.0 | `09a8f19` (the commit `contracts/pinned.json` pins) | `AMR_FLEET_CONTROL_IMAGE` |

`AMR_FLEET_CONTROL_MAP` names amr-fleet-control's `config/map.example.yaml` at that commit; the test adds the nodes
`PK1`, `FWD1` and `CLR1` to it. Every container (Postgres, Kafka, Mosquitto, the three subsystems) joins the test
binary's one network (`internal/testing/dockernet`): there the broker is `kafka:9092`
(`kafkatest.InternalBrokers`) and Postgres is `postgres:5432` (`pgtest.NetworkURL`), while the test process uses the
mapped `127.0.0.1` ports. asrs-control and amr-fleet-control each get an empty database (`pgtest.EmptyURL`) and run
their own migrations at boot. The floor files are `cmd/api/testdata/e2e/`: `world.yaml` (the emulator),
`asrs-layout.yaml` (asrs-control's layout: the world's `asrs` block, held equal by `TestTheFloorFilesDescribeOneFloor`)
and `flow-layout.yaml` (byte-equal to `config/flow-layout.example.yaml`).

The rows, in order, asserted on the REST answers and on `warehouse.material-flow-control.events`: putaway of TOTE-1..3
from the decant handoff (track `IN_ASRS` at `WH1-AUTO-ASRS-ASRS1-00-00-S`), goods-to-person (PICK TOTE-1 to ST1, queue
position 1), idempotency (the same `transport_request_id` under a new key: exactly one `TransportAccepted` carries that
request id), rejection (`422 CONTAINER_BUSY`, `TransportRejected`, no order), replenishment (TOTE-2 to `FWD1`, track
`AT_HANDOFF` at `WH1-FWD-FWD1`), the return of TOTE-1 (track `IN_ASRS` again), outbound (`FWD1` to `PK1`, track
`AT_HANDOFF` at `WH1-PCK-PK1`), clarification (`ASRS_NO_READ`: `TransportFailed` `CLARIFICATION`,
`ContainerSentToClarification` `NO_READ`, the track `AT_CLARIFICATION` at `WH1-CLR-CLR1`) and the capacity loop
(`ROBOT_DISCONNECT` of R2: `AMR_FLEET/AMR1` `DEGRADED`, one unit fewer, within 60 s). At the end, one read of the run's
facts requires each of its 8 orders to have exactly one outcome. On a failure the test prints each container's log tail.

Every fact wait is positional (ruling P4-R19): it reads from a mark, `kafkatest.EndOffset` taken BEFORE its stimulus
(`kafkatest.ReadFrom`), never from offset 0, so no older fact (the boot ramp, an earlier `-count` run) can satisfy it.
The capacity loop requires the last fleet fact before its mark to be the idle fleet (`AVAILABLE` 3 of 3), and the
`DEGRADED` fact to come after it.
Before the first row the floor waits until material-flow-control's consumer groups have committed both subsystem
topics up to their end offsets (`kafkatest.Committed`) and `GET /subsystems` shows `AMR1` `AVAILABLE` 3 of 3: with
`-count=N` a new group replays the earlier runs' facts, their boot ramps included.

CI's `integration` job checks out the three commits, builds the images and sets the four variables. Locally (an arm64
host builds natively; the published images are amd64-only):

```bash
git -C ../equipment-emulator archive b58201e14342c99f5033327214ea317eb2a17962 | docker build -t equipment-emulator:e2e -
git -C ../asrs-control archive a3e172d34131928fb5178380e6664d8a117c5ed2 | docker build -t asrs-control:e2e -
git -C ../amr-fleet-control archive 09a8f1915be5129efbb240dd6730fec640f93635 | docker build -t amr-fleet-control:e2e -
mkdir -p .e2e/amr-fleet-control/config
git -C ../amr-fleet-control show 09a8f1915be5129efbb240dd6730fec640f93635:config/map.example.yaml > .e2e/amr-fleet-control/config/map.example.yaml
EQUIPMENT_EMULATOR_IMAGE=equipment-emulator:e2e ASRS_CONTROL_IMAGE=asrs-control:e2e \
  AMR_FLEET_CONTROL_IMAGE=amr-fleet-control:e2e AMR_FLEET_CONTROL_MAP=$PWD/.e2e/amr-fleet-control/config/map.example.yaml \
  go test -tags integration ./cmd/api/ -run TestTheEightAcceptanceRows -count=1 -timeout 25m -v
```

`-timeout 25m` (also on CI's integration step): the test's waits sum to about 24 minutes at worst (the budget comment
above the test), more than `go test`'s 10-minute default; a run on the emulator floor takes about a minute.

Raising a pin is a deliberate change (a new commit in `ci.yml` and here, the test rerun), never `develop`.

## Consumer contract tests

`contracts/pinned.json` lists the four producer files this repo pins (asrs-control and amr-fleet-control, AsyncAPI
and OpenAPI each); CI's `pinned-contracts` job fails when a copy differs from the producer's `develop`. The tests
beside each adapter read the pinned copies:

| Test file | Proves |
| --- | --- |
| `internal/adapters/inbound/kafka/pinned_asrs_test.go`, `pinned_amr_test.go` | every pinned example of a consumed type is applied by the production handler; every field the consumer decodes exists in the pinned schema; the capacity mapping fields exist and `robots_total` may be 0 |
| `internal/adapters/inbound/kafka/required_fields_test.go` | every field the pinned schema requires is required (poison without it), the optional ones may be absent, the enums are the pinned ones, and the amr-fleet-control fixtures satisfy the pinned schema |
| `internal/adapters/outbound/amr/pinned_test.go` | the paths exist, the leg request sends only declared properties and every required one, the `Leg` answer carries what the client reads, and `amr.Retryable` equals the pinned RETRYABLE refusals |
| `internal/adapters/inbound/kafka/pinned_channels_test.go` | `AsrsTopic` and `AmrTopic` are integration channels of the pinned AsyncAPI and every handled type resolves through that channel's `subscribe.message.oneOf` |
| `internal/adapters/inbound/kafka/pinned_reasons_test.go` | the LegFailed reasons the consumer accepts are exactly the pinned enum |
| `internal/adapters/outbound/amr/pinned_idempotency_test.go`, `internal/adapters/outbound/asrs/pinned_idempotency_test.go` | every POST the client sends declares the `Idempotency-Key` header, required, long enough for the longest key the dispatcher mints |

## Fitness tests (`internal/architecture`)

| Sensor | Fails when |
| --- | --- |
| `architecture_test.go` (arch-go) | a layer imports what the hexagonal rule forbids |
| `fitness_test.go` | a fleet invariant learned from an incident breaks (for example an auth middleware reintroduced) |
| `no_equipment_link_test.go` | any package imports an MQTT client (this service never speaks to equipment) |
| `runtime_no_memory_fitness_test.go` | a `cmd/` binary imports the in-memory doubles |
| `no_go_files_at_root_fitness_test.go` | a `.go` file sits at the repository root |
| `agent_readonly_fitness_test.go`, `agent_readonly_methods_fitness_test.go` | `cmd/mcp`, `cmd/a2a` or their adapters reference a write seam, import a raw driver (`pgx`, `database/sql`), alias or dot-import a guarded package, or select a write method (`Save`, `Insert`, `Exec`, `Lock`, ...) |
| `mcp_authority_fitness_test.go` | an MCP tool or A2A skill does not declare exactly one authority level, or a read tool is not read-only |
| `mcp_optional_inputs_test.go` | an optional MCP tool input is published as required (no `omitempty`) |
| `kafka_reader_fitness_test.go` | a consumer group is a literal, or a per-process group lacks `CommitInterval` |
| `kafka_writer_acks_fitness_test.go`, `kafka_writer_timeout_fitness_test.go`, `kafka_writer_topic_fitness_test.go` | a kafka-go writer lacks `RequiredAcks`, lacks `WriteTimeout`/`ReadTimeout`, or sets a `Topic` its messages also set |
| `kafka_dlq_shutdown_fitness_test.go` | a dead-letter send does not guard `ctx.Err()` |
| `outbox_relay_cap_fitness_test.go` | the composition root builds the outbox relay without its attempt cap |
| `events_fitness_test.go` | an envelope other than CloudEvents 1.0 (or an envelope toggle) appears |
| `catalogue_fitness_test.go` | a type `apis/asyncapi.yaml` declares for this service is missing from the CloudEvents ADR's type catalogue |
| `idempotency_template_test.go` | template-mode only (it skips in this repository): runs the fleet replay-class reference test against a toy module, whose `TOY_MUTATION` environment variable selects the deliberately broken variant the sensor must catch |

The replay class of every REST problem type is pinned by `internal/adapters/inbound/http/errors_test.go`.

## Mutation testing

Gremlins thresholds (efficacy and mutant coverage) are measured per package and written into `.gremlins.yaml` as plain
numbers before the first release; they are never set above the measurement and never lowered later.
