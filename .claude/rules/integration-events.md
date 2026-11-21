---
paths:
  - "internal/adapters/**/kafka/**"
  - "internal/adapters/outbound/events/**"
  - "apis/asyncapi*"
---

# Integration events

## Events: CloudEvents 1.0 is MANDATORY

Every Kafka message this service produces or consumes (integration
`warehouse.<ctx>.events` AND analytics `warehouse.<ctx>.analytics`) is a
CloudEvents 1.0 event in structured content mode. This is a hard fleet rule,
not a preference — there is nothing to "choose" here:

- No flat envelope (`event_id`/`event_type`/`occurred_at`), no dual-write,
  no dual-read, no envelope toggle env var (`EVENT_ENVELOPE_MODE` is gone
  fleet-wide). `internal/architecture/fitness_test.go`'s
  `TestNoEventEnvelopeToggleOrFlatEnvelope` fails CI on any of them.
- Build/validate/(un)marshal with `github.com/cloudevents/sdk-go/v2/event`
  (latest v2) via ONE helper package,
  internal/adapters/kafka/cloudevents/ — copy it from the harness template's
  `templates/cloudevents/cloudevents.go.tmpl` (+ its test) and change only
  the per-repo constants. Transport stays `segmentio/kafka-go` (no sdk-go
  protocol/client packages, no hand-rolled CloudEvent structs).
- Every produced message carries the Kafka header
  `content-type: application/cloudevents+json; charset=UTF-8`
  (`cloudevents.ContentTypeHeader()`), next to the W3C trace headers
  (`traceparent`/`tracestate` stay in headers, never duplicated into
  extension attributes). Message key = aggregate id, `kafkago.Hash{}`
  balancer.
- Required attributes: `specversion=1.0`; `id` (UUID v4 minted ONCE per
  domain event and persisted with the outbox row, so redelivery carries
  the same id); `source=/warehouse/<repo>`; `type`; `subject` (aggregate
  instance id, never empty); `time` (domain occurred-at, UTC);
  `datacontenttype=application/json`;
  `dataschema=urn:warehouse:<repo>:<events|analytics>:<EventName>:v<N>`.
  No custom extension attributes without an ADR.
- `type` = `com.warehouse.<subdomain>.<bounded-context>.<entity>.<EventName>`
  (this repo: `com.warehouse.wes.material-flow-control`). The SAME `type` names the
  occurrence on both the integration and the analytics topic; `dataschema`
  names the payload shape. Breaking payload change => new `.v2` type + new
  dataschema version, never mutate an existing one.
- Consumers decode with `cloudevents.Decode` (validates), dispatch on the
  FULL `type` string (never a short name or suffix match), ignore unknown
  types, read `time`/`subject` from attributes and the payload via
  `DataAs`, dedupe on `id`, and DLQ/skip — WARN log + commit past, never
  crash, never block the partition, never fall back to parsing a legacy
  shape — anything that fails CloudEvents validation.
- Tests: a golden exact-JSON test per published `type` (all attributes +
  the `content-type` header); a legacy-flat-message-rejected test per
  consumer; Kafka integration tests via testcontainers only.

Full standard, subdomain table and the fleet's cross-service type
catalogue: warehouse-docs docs/strategic-design/event-standard-cloudevents.md.
In this repo ADR 0003 records the envelope with the seven published facts.

## This service

- Published: the seven types of ADR 0003, through `internal/adapters/outbound/events` into the outbox, on BOTH
  `warehouse.material-flow-control.events` and `.analytics` with one id. Key = subject. `data` snake_case.
- Consumed: the five asrs-control and the five amr-fleet-control types of ADR 0004, dispatched on the FULL type;
  everything else committed past. FetchMessage + CommitMessages after success; inbox claim (`processed_events`) in
  the effect's unit of work; transient errors retried (5 attempts, 200 ms → 5 s) then `<topic>.dlq`; malformed
  payloads dead-lettered at once; `ctx.Err()` checked before every dead-letter call.
- Consumer groups come from `MFC_ASRS_CONSUMER_GROUP` / `MFC_AMR_CONSUMER_GROUP`, never a literal. Both are
  fixed-group consumers: `CommitInterval` and `MaxWait` stay unset.
- Producer contracts are pinned in `contracts/` (`pinned.json`); refresh by copying, never by editing.
