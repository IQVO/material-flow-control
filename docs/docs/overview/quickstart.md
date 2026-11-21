---
id: quickstart
title: "Quickstart"
sidebar_label: "Quickstart"
description: "Run material-flow-control locally against Postgres, Kafka and the two subsystem controllers, and send one transport request."
---

# Quickstart

`cmd/api` runs only against real infrastructure: Postgres, Kafka and the two subsystem controllers. A missing
mandatory variable is a boot error that names it (one JSON line, exit code 1).

## 1. The floor

The end-to-end floor is Postgres 16, `confluentinc/confluent-local:7.6.1` (Kafka), Eclipse Mosquitto 2.0.20 (the
subsystems' PLC link, not used by this service), `IQVO/equipment-emulator` (the simulated AS/RS and robots),
`IQVO/asrs-control` built from `007bf7b` and `IQVO/amr-fleet-control` built from its `develop`. Start the
infrastructure:

```bash
docker run -d --name mfc-pg -e POSTGRES_PASSWORD=mfc -e POSTGRES_DB=mfc -p 5432:5432 postgres:16
docker run -d --name mfc-kafka -p 9092:9092 confluentinc/confluent-local:7.6.1
docker run -d --name mfc-mqtt -p 1883:1883 eclipse-mosquitto:2.0.20 mosquitto -c /mosquitto-no-auth.conf
```

Then start the emulator and the two subsystem controllers from their own checkouts, as their quickstarts describe
(asrs-control on `:8081` and amr-fleet-control on `:8082` below). Their layout and map must name the same ids as this
service's layout file: `config/flow-layout.example.yaml` uses AS/RS `ASRS1` with ports `IN1`, `OUT1`, `IN2`, `OUT2`
and fleet `AMR1` with handoff points `ASRS1-IN1`, `ASRS1-OUT1`, `ASRS1-IN2`, `ASRS1-OUT2`, `ST1`, `ST2`, `PK1`, `FWD1`
and `CLR1`.

## 2. material-flow-control

```bash
export DATABASE_URL='postgres://postgres:mfc@localhost:5432/mfc?sslmode=disable'
export KAFKA_BROKERS=127.0.0.1:9092
export ASRS_CONTROL_URL=http://localhost:8081
export AMR_FLEET_CONTROL_URL=http://localhost:8082
MFC_LAYOUT_FILE=config/flow-layout.example.yaml go run ./cmd/api
```

The migrations run at boot; `GET /readyz` answers `200` once Postgres and Kafka answer.

## 3. One transport request

```bash
curl -i -X POST http://localhost:8080/transport-requests \
  -H 'Content-Type: application/json' -H 'Idempotency-Key: quickstart-1' \
  -d '{"transport_request_id":"task-1.1","container_id":"TOTE-1","source":"WH1-AUTO-ASRS-ASRS1-00-00-S",
       "destination":"ST1","purpose":"PICK","priority":"NORMAL","due_by":"2026-11-21T12:00:00Z"}'
```

The answer is `202` with the `TransportOrder` view and `Location: /transport-requests/task-1.1` when `TOTE-1` is
stored in `ASRS1` (this service learned it from asrs-control's `ContainerStored`), or `422` with
`reason: UNKNOWN_CONTAINER` when it never was. Sending the same command again with the same key answers the stored
answer with `Idempotency-Replayed: true`. Progress arrives on `warehouse.material-flow-control.events`
(`TransportAccepted`, `ContainerArrivedAtStation`, `TransportCompleted`); `GET /transport-requests/task-1.1` is for
operators, never for polling.

## The read-only agents

```bash
DATABASE_URL=$DATABASE_URL MFC_LAYOUT_FILE=config/flow-layout.example.yaml go run ./cmd/mcp
DATABASE_URL=$DATABASE_URL MFC_LAYOUT_FILE=config/flow-layout.example.yaml A2A_AGENT_URL=http://localhost:8093/ go run ./cmd/a2a
```
