---
id: contributing
title: "Contributing"
sidebar_label: "Contributing"
description: "GitFlow, commits, the gates to run before a push, and what must change together."
---

# Contributing

## GitFlow

`develop` is the default branch and `main` the release branch. Work happens on `feature/*` branches and reaches
`develop` through a pull request whose required checks are green; `develop` reaches `main` through a release pull
request. Nobody pushes to `develop` or `main` directly, and no hook is ever bypassed (`--no-verify` is banned).

## Before a push

```bash
make check-fast            # format, vet, fitness tests, changed packages
make check                 # fmt-check, vet, build, lint, test (the pre-push hook runs it)
go vet -tags integration ./...
make coverage              # >= 90% over domain and application
```

Commits are conventional (`feat(...)`, `fix(...)`, `test(...)`, `docs: ...`); a test goes in first when behaviour
changes.

## What changes together

| When you change | Also change |
| --- | --- |
| a REST route, body or problem | `apis/openapi.yaml`, then regenerate the reference: `cd docs && npm run clean-api-docs material-flow-control && npm run gen-api-docs material-flow-control` (the `docs-api-drift` job fails otherwise) |
| an event payload | `apis/asyncapi.yaml`, the encoder's golden test, [Events published](/docs/ecosystem/events-published) |
| a consumed producer contract | the pinned copy under `contracts/` (copy the producer's `develop` file, never edit it) |
| the layout | asrs-control's layout and amr-fleet-control's map, in the same change |
| a domain `Snapshot` field | a numbered migration rewriting the JSONB columns when the field is renamed or retyped |
| an architectural decision | a new ADR under `docs/adr/` and its row in `docs/adr/index.md` |

The docs site builds with `cd docs && npm ci && npm run build` (Node 20); `onBrokenLinks` is `throw`.
