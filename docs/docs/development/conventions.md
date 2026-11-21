---
id: conventions
title: "Conventions"
sidebar_label: "Conventions"
description: "The hexagonal rule, the file and function budgets, the REST and event conventions and the replay classes."
---

# Conventions

## The hexagonal rule

`internal/domain/**` imports only the standard library and `internal/domain/**`; `internal/application/**` only the
domain and the application; inbound and outbound adapters never import each other in production code; only `cmd/`
wires. The domain has no JSON tags, SQL or HTTP types. `internal/application/ports` holds interfaces, sentinel errors
and the `Refusal` type only; value types live in `facts`, `command`, `idempotency` and `outbox`.

## Budgets

A production `.go` file holds at most 300 lines of code; `funlen` 80 lines / 50 statements, `cyclop` 15, `gocognit`
20. Split a file or a function, never raise a limit.

## REST

The fleet REST guideline: plural nouns, no colon verbs, a sub-resource for a command on a resource
(`.../cancellation`, `.../clarification-resolution`), `Idempotency-Key` on every POST, snake_case JSON,
`application/problem+json` errors with a stable `code`, no authentication. The relationship-1 interface is in
[The relationship-1 interface](/docs/ecosystem/relationship-1-rest).

## Replay classes

Every problem type declares a replay class in `internal/adapters/inbound/http/errors.go`, and a fitness test fails one
that does not:

| Class | Stored? | Meaning |
| --- | --- | --- |
| FINAL | yes, replayed for 24 h | a function of the request alone (malformed, purpose rule, conflict) |
| OUTCOME | yes, with its writes | the four rejections: the rejection record and `TransportRejected` commit WITH the stored answer |
| RETRYABLE | no, the key is released | state-dependent refusals and not-founds the retry can outrun, 5xx |

2xx answers are stored; 5xx answers and panics never are. The fleet rule is `docs/idempotency-replay-classes.md`.

## Events

CloudEvents 1.0 structured mode only, through `internal/adapters/kafka/cloudevents`; type prefix
`com.warehouse.wes.material-flow-control.`; the entity segment is the raising aggregate; every event goes to both
`warehouse.material-flow-control.events` and `.analytics` under one id; Kafka key = subject; `data` snake_case; every
`*_at` is this context's own clock. No payload names a robot, leg, node, shuttle, aisle, rack position, port or
storage job.

## Time

A subsystem's own time (`plc_timestamp`, `robot_timestamp`, `measured_at`, `changed_at`) is decoded for logs but never
copied into a published `*_at`.
