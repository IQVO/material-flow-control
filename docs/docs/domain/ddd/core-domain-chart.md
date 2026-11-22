---
id: core-domain-chart
title: "Core domain chart"
sidebar_label: "Core domain chart"
description: "Where material-flow-control sits on the core domain chart: business differentiation against model complexity."
---

# Core domain chart

| Context | Business differentiation | Model complexity | Classification |
| --- | --- | --- | --- |
| `material-flow-control` | medium: how this site joins its subsystems and sequences its stations | high: four aggregates, multi-leg routes, out-of-order facts, retries, clarification | **Supporting** |
| `asrs-control` | low: standard AS/RS control behind a telegram protocol | high | Generic |
| `amr-fleet-control` | low: standard fleet control behind VDA 5050 | high | Generic |

The complexity is accidental to the floor, not to the business: it is kept inside this context so the WES sees one
simple contract (relationship 1). See [Subdomain classification](/docs/domain/subdomain-classification).
