---
id: routing-and-handoffs
title: "Routing and handoffs"
sidebar_label: "Routing and handoffs"
description: "How a source and a destination become legs through handoff points, and the purpose rules a request must follow."
---

# Routing and handoffs

The WES chooses the container and the destination; the WCS chooses the route (seam decision 3). The WES never names
a port, a robot or a leg. `internal/domain/routing` is a pure policy over the layout's places.

## Places a request may name

| Place | Named by |
| --- | --- |
| the AS/RS | its logical `LocationCode` (`WH1-AUTO-ASRS-ASRS1-00-00-S`); never a port code (a port is `DESTINATION_UNREACHABLE`) |
| a goods-to-person station | its station id (`ST1`) or its `LocationCode` |
| a handoff (decant, pack, dock, forward area) or a clarification point | its `LocationCode` |

## Routes (decision D3)

| From | To | Legs |
| --- | --- | --- |
| the AS/RS | an AMR place | `RETRIEVE` (asrs-control picks the output port: `port_id` omitted), then `MOVE` from the presented port to the destination |
| an AMR place | the AS/RS | `MOVE` to an input port chosen at dispatch (asrs-control's `recommended_input_port_id`, else the layout's first `INPUT` port), then `STORE` on that port |
| an AMR place | an AMR place | one `MOVE` |
| the AS/RS | the AS/RS | `DESTINATION_UNREACHABLE` |
| anything not a layout place | | `DESTINATION_UNREACHABLE` |

An AMR place is a station, a handoff or a clarification point; robots pick and drop at its `handoffPointId`.

## Handoffs are confirmed by the subsystem that held the container (decision D9)

A leg starts only after the previous leg's outcome fact: a `MOVE` after `ContainerPresentedAtPort` with
`scan_result` `READ` (or after acceptance when it is the first leg), a `STORE` after the AMR `LegCompleted` at the
input port. On amr-fleet-control's `ContainerPickedUp` for a leg that started at an AS/RS OUTPUT port, a
`CONFIRM_PICKUP` command calls `POST /asrs/{asrsId}/ports/{portId}/pickups`, so asrs-control can present the next
container at that port (decision D10).

## Purpose rules (decision D4)

| Purpose | Source | Destination |
| --- | --- | --- |
| `PICK` | any | a station |
| `PUTAWAY` | not the AS/RS | the AS/RS |
| `RETURN` | not the AS/RS | the AS/RS (routes like `PUTAWAY`; only the WES treats it differently) |
| `REPLENISH` | the AS/RS | a station or a handoff |
| `OUTBOUND` | not the AS/RS | a station or a handoff |

A request that breaks its purpose rule, or whose source equals its destination, is `400 invalid-parameter` (FINAL):
the request alone decides it.

## The accept-or-reject decision (decision D5)

From local state only, inside one unit of work, in this order: (1) a source or destination that is not a layout
place, or no route: `DESTINATION_UNREACHABLE`; (2) the container has an active transport order or is held at
clarification: `CONTAINER_BUSY`; (3) the container is not known at the source (the source is the AS/RS and its track
is not `IN_ASRS` there, or its track places it inside the automation somewhere other than the source):
`UNKNOWN_CONTAINER`; (4) a subsystem on the route whose latest capacity snapshot is `UNAVAILABLE`:
`SUBSYSTEM_UNAVAILABLE`. No subsystem is called before the answer; the dispatcher requests the first leg after commit.
