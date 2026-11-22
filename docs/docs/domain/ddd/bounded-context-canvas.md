---
id: bounded-context-canvas
title: "Bounded context canvas"
sidebar_label: "Bounded context canvas"
description: "The ddd-crew bounded context canvas of material-flow-control."
---

# Bounded context canvas

| Section | Content |
| --- | --- |
| **Name** | material-flow-control |
| **Purpose** | Turn a WES transport request into a completed physical move of a container across the automated subsystems, and be the one source of truth for where each container physically is inside the automation. |
| **Strategic classification** | Supporting; role: execution / gateway (the one door from the WES to the automation) |
| **Domain roles** | Execution context; Open Host Service of relationship 1 |
| **Inbound communication** | REST from fulfillment-execution (`POST /transport-requests`, `.../cancellation`); operators (`.../clarification-resolution`); Kafka facts from asrs-control (5 types) and amr-fleet-control (5 types) |
| **Outbound communication** | REST to asrs-control (storage jobs, pickups) and amr-fleet-control (legs); Kafka `warehouse.material-flow-control.events` and `.analytics` (7 types) |
| **Ubiquitous language** | TransportRequest, TransportOrder, Leg, HandoffPoint, ContainerTrack, StationQueue, ClarificationPoint, Purpose ([Ubiquitous language](/docs/domain/ubiquitous-language)) |
| **Business decisions** | accept or reject at once from local state (D5); route through handoff points (D3); start a leg only after the previous handoff fact (D9); retry or re-plan a failed leg (D13); admit station arrivals by priority then due (D11); hold a doubtful container (D12) |
| **Assumptions** | the container registry can be learned from asrs-control's `ContainerStored` facts within the topic's retention; one api process |
| **Verification metrics** | `material_flow_control.transports.ended` by outcome and reason; outbox backlog and lag |
| **Open questions** | which capacity fields bound goods-to-person release (wes-work-planning, plan 05b); whether exception-management resolves clarifications automatically |
