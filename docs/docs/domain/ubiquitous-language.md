---
id: ubiquitous-language
title: "Ubiquitous language"
sidebar_label: "Ubiquitous language"
description: "The terms of material-flow-control as the code and the contracts use them."
---

# Ubiquitous language

| Term | Meaning | In the code |
| --- | --- | --- |
| **TransportRequest** | The WES's request to move one container: source, destination, purpose, priority, due. Identified by `transport_request_id`; it has ONE outcome, ACCEPTED or REJECTED. | `internal/domain/transportrequest` (`Record`) |
| **TransportOrder** | This context's accepted plan for one request: a sequence of legs and one outcome. Not a customer order and not a VDA 5050 order. | `internal/domain/transportorder` (`Order`) |
| **Internal order** | A TransportOrder this context creates itself (one MOVE of a held container from an AS/RS output port to a clarification point); it publishes no transport event. | `transportorder.NewInternal` |
| **Leg** | One move inside one subsystem, from one handoff point to the next: an AS/RS `RETRIEVE` or `STORE`, an AMR `MOVE`. | `transportorder.Leg`, `routing.LegKind` |
| **Leg reference** | `<orderId>.<seq>.<attempt>`: sent as the subsystem request's `reference`, the base of its Idempotency-Key, and the key every subsystem fact is matched by. | `Leg.Ref` |
| **HandoffPoint** | A place where a container passes from one subsystem to another: an AS/RS port, an AMR pickup or drop node at a station, a handoff or a clarification point. | layout `handoffPointId` |
| **Place** | Anything the layout names: the AS/RS (`ASRS`), an AS/RS port (`ASRS_PORT`), a station (`STATION`), a handoff (`HANDOFF`), a clarification point (`CLARIFICATION`). | `layout.Place`, `layout.PlaceKind` |
| **ContainerTrack** | The physical position of one container, its active order, its clarification hold and its move history. | `internal/domain/containertrack` (`Track`) |
| **Position** | `IN_ASRS`, `AT_PORT`, `ON_ROBOT`, `AT_STATION`, `AT_HANDOFF`, `AT_CLARIFICATION` or `UNKNOWN`, with a code (location code, station id, or the fleet id for `ON_ROBOT`). | `containertrack.PositionKind` |
| **StationQueue** | The occupants (admitted containers, in position order) and the waiting arrivals of one goods-to-person station, under its buffer limit. | `internal/domain/stationqueue` (`Queue`) |
| **Buffer limit** | How many containers a station holds at once; this context's configuration (`stations[].bufferLimit`). | layout |
| **ClarificationPoint** | Where a container goes when it cannot be identified or does not match its order. | layout `clarificationPoints[]` |
| **Clarification hold** | The state of a track that must not be dispatched until resolved, with its reason (`NO_READ`, `MISMATCH`, `CONTAINER_NOT_AT_SOURCE`, `CONTAINER_NOT_AT_PORT`, `STORAGE_MAP_MISMATCH`, `CONTAINER_ON_FAILED_ROBOT`). | `containertrack.ClarificationReason` |
| **Purpose** | Why a container moves: `PICK`, `PUTAWAY`, `REPLENISH`, `OUTBOUND` or `RETURN`. | `transportrequest.Purpose` |
| **Rejection** | One of the four immediate refusals of a request: `UNKNOWN_CONTAINER`, `CONTAINER_BUSY`, `DESTINATION_UNREACHABLE`, `SUBSYSTEM_UNAVAILABLE`. | `transportrequest.Reason` |
| **Subsystem** | One equipment system under this context: the AMR fleet (`AMR_FLEET`) or the AS/RS (`ASRS`). | `routing.Subsystem` |
| **Subsystem command** | A queued call to a subsystem (`REQUEST_LEG`, `CANCEL_LEG`, `CONFIRM_PICKUP`) the dispatcher sends after commit. | `internal/application/command`, `subsystem_commands` |
| **Capacity snapshot** | The latest published capacity of one subsystem or station. | `internal/domain/capacity` |
| **Flow lock** | The single row every write unit of work locks first, so all writes are serial. | `flow_lock`, `ports.FlowLock` |
| **Replay class** | How an `Idempotency-Key` answers a retry: FINAL, OUTCOME or RETRYABLE. | `internal/adapters/inbound/http/errors.go` |
