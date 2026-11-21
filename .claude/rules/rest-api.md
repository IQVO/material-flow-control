---
paths:
  - "internal/adapters/inbound/http/**"
  - "apis/openapi*.yaml"
  - "apis/openapi/**"
---

# REST API rules (relationship 1, the Open Host Service)

- Routes: `POST /transport-requests`, `GET /transport-requests/{transportRequestId}`,
  `POST /transport-requests/{transportRequestId}/cancellation`, `GET /container-tracks/{containerId}`,
  `POST /container-tracks/{containerId}/clarification-resolution`, `GET /stations/{stationId}`, `GET /subsystems`,
  `GET /healthz`, `GET /readyz`. No other route without an ADR; never a colon verb.
- Every POST: `Idempotency-Key` (1-128), the fleet middleware with replay classes; the four 422 rejections are
  OUTCOME (KeepWrites), field errors FINAL, state-dependent refusals RETRYABLE. A problem without a class fails
  `errors_test.go`.
- Bodies snake_case; `application/problem+json` with `code` (and `reason` + `transport_request_id` on a 422).
- No robot, node, shuttle, aisle, rack position, storage job or leg in a relationship-1 body.
- `apis/openapi.yaml` is the contract: Spectral (`.spectral.yaml`) and the docs site's API pages read it.
