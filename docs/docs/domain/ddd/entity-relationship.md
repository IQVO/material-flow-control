---
id: entity-relationship
title: "Entity relationship"
sidebar_label: "Entity relationship"
description: "The Postgres tables of material-flow-control and how they relate."
---

# Entity relationship

```mermaid
erDiagram
  transport_requests ||--o| transport_orders : "ACCEPTED names"
  transport_orders ||--|{ transport_legs : "leg refs"
  transport_orders ||--o{ subsystem_commands : "commands"
  container_tracks ||--o{ container_moves : "history"
  container_tracks }o--o| transport_orders : "active order"
  station_queues ||--o{ transport_orders : "entries (in the snapshot)"
```

Standalone tables: `flow_lock` (one row), `subsystem_capacities`, `equipment_units`, `processed_events` (the inbox),
`outbox_events` (the outbox) and `idempotency_keys`. Aggregates are JSONB snapshots (`record`, `snapshot`, `move`,
`state`); the other columns exist for lookups and indexes. The full list is on
[Architecture](/docs/overview/architecture#storage); the schema is `migrations/0001_material_flow_control_schema.up.sql`.
