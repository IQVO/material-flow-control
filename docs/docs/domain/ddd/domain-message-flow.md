---
id: domain-message-flow
title: "Domain message flow"
sidebar_label: "Domain message flow"
description: "The message flow of a goods-to-person transport from the WES request to its completion."
---

# Domain message flow

A PICK from the AS/RS to station `ST1`:

```mermaid
flowchart TD
  A["WES: POST /transport-requests (PICK)"] --> B["RequestTransport: accept"]
  B --> C["TransportAccepted"]
  B --> D["REQUEST_LEG RETRIEVE (command outbox)"]
  D --> E["asrs-control: POST /asrs/ASRS1/storage-jobs"]
  E --> F["ContainerPresentedAtPort READ at OUT1"]
  F --> G["StationQueue ST1: join, admit when a slot is free"]
  G --> H["REQUEST_LEG MOVE OUT1 to ST1"]
  H --> I["amr-fleet-control: POST /fleets/AMR1/legs"]
  I --> J["LegAssigned, ContainerPickedUp"]
  J --> K["CONFIRM_PICKUP to asrs-control (port OUT1 freed)"]
  J --> L["LegCompleted at ST1"]
  L --> M["ContainerArrivedAtStation (queue_position)"]
  M --> N["TransportCompleted"]
```

The failure paths (no-read, leg failure, timeout, cancellation) are on [Transport lifecycle](/docs/domain/transport-lifecycle)
and [Clarification](/docs/domain/clarification).
