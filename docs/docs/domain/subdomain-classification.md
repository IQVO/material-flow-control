---
id: subdomain-classification
title: "Subdomain classification"
sidebar_label: "Subdomain classification"
description: "Why material-flow-control is a Supporting subdomain of the WCS tier."
---

# Subdomain classification

**Supporting** ([ADR 0001](/docs/adr/0001-material-flow-control-bounded-context)).

| Question | Answer |
| --- | --- |
| Is it where the warehouse competes? | No: order promise, slotting and labour planning are. Moving a tote reliably is necessary, not differentiating. |
| Is it specific to this warehouse? | Yes: its layout, its handoff points, its station buffers and its purpose rules are this site's material flow, so it cannot be bought as-is. |
| Is it generic? | No: unlike `asrs-control` and `amr-fleet-control` (Generic subsystem controllers behind standard protocols), it encodes how this warehouse joins its subsystems. |

The WCS tier holds three contexts: `material-flow-control` (Supporting, the one door from the WES),
`asrs-control` and `amr-fleet-control` (the subsystem controllers it calls). The CloudEvents segment is `wes`, like
the other WCS contexts.
