---
id: overview
title: "API overview"
sidebar_label: "Overview"
slug: /api-reference
description: "The interfaces of material-flow-control (REST, Kafka events, MCP, A2A), the endpoint table and where each contract lives."
---

# API overview

| Interface | Source of truth | Notes |
| --- | --- | --- |
| REST (relationship 1) | `apis/openapi.yaml` | The pages under *REST* are generated from the spec with `npm run gen-api-docs material-flow-control`; do not edit them by hand (the `docs-api-drift` CI job fails on a difference). Caller: fulfillment-execution. Rules: [The relationship-1 interface](/docs/ecosystem/relationship-1-rest). |
| Events | `apis/asyncapi.yaml` | Seven published CloudEvents types ([Events published](/docs/ecosystem/events-published)); ten consumed ([Events consumed](/docs/ecosystem/events-consumed)). |
| MCP, A2A | the adapters' code | Read-only, authority L1 ([Agents](/docs/ecosystem/agents)). |

## Endpoints

| Endpoint | Operation id | Success |
| --- | --- | --- |
| `POST /transport-requests` | `requestTransport` | `202` the `TransportOrder`, or `422` a rejection |
| `GET /transport-requests/{transportRequestId}` | `getTransport` | `200` the `TransportOrder` (status `REJECTED` for a rejected id) |
| `POST /transport-requests/{transportRequestId}/cancellation` | `cancelTransport` | `200` cancelled now, `202` asked of the subsystem |
| `GET /container-tracks/{containerId}` | `getContainerTrack` | `200` the `ContainerTrack` |
| `POST /container-tracks/{containerId}/clarification-resolution` | `resolveClarification` | `200` the `ContainerTrack` |
| `GET /stations/{stationId}` | `getStation` | `200` the `StationQueue` |
| `GET /subsystems` | `listSubsystems` | `200` every capacity snapshot |
| `GET /healthz`, `GET /readyz` | `healthz`, `readyz` | `200`; `/readyz` `503` while draining or a dependency is down |
