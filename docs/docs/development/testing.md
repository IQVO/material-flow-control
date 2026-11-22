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
| Integration | `make integration` (`go test -tags integration ./...`) | the Postgres repositories, the flow lock and its timeouts, the Idempotency middleware over real Postgres, the consumers and the relay over real Kafka, `cmd/api` traces and outbox gauges |
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
before a consumer under test is built. CI's `integration` job is their gate.

## The end-to-end test

:::note
The end-to-end test is not in the repository yet (Task 25 of the build plan adds it). This section states what it
will run.
:::

The end-to-end test (`cmd/api/e2e_subsystems_integration_test.go`) will run material-flow-control in-process against
the REAL `asrs-control`, `amr-fleet-control` and `equipment-emulator` built from pinned commits, with Postgres, Kafka
and Mosquitto in testcontainers, and no in-test subsystem fake; `amr-fleet-control` is built from `09a8f19`, the
commit `contracts/pinned.json` pins. amr-fleet-control is merged and its two contracts are pinned, so until the
end-to-end test lands its client and consumer are proven against the pinned copies (see
[Consumer contract tests](#consumer-contract-tests)).

## Consumer contract tests

`contracts/pinned.json` lists the four producer files this repo pins (asrs-control and amr-fleet-control, AsyncAPI
and OpenAPI each); CI's `pinned-contracts` job fails when a copy differs from the producer's `develop`. The tests
beside each adapter read the pinned copies:

| Test file | Proves |
| --- | --- |
| `internal/adapters/inbound/kafka/pinned_asrs_test.go`, `pinned_amr_test.go` | every pinned example of a consumed type is applied by the production handler; every field the consumer decodes exists in the pinned schema; the capacity mapping fields exist and `robots_total` may be 0 |
| `internal/adapters/inbound/kafka/required_fields_test.go` | every field the pinned schema requires is required (poison without it), the optional ones may be absent, the enums are the pinned ones, and the amr-fleet-control fixtures satisfy the pinned schema |
| `internal/adapters/outbound/amr/pinned_test.go` | the paths exist, the leg request sends only declared properties and every required one, the `Leg` answer carries what the client reads, and `amr.Retryable` equals the pinned RETRYABLE refusals |

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
