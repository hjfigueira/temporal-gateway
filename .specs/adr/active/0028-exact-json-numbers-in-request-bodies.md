# ADR-028: Exact JSON numbers in request bodies

**Status:** Accepted. Extends [ADR-004](0004-origin-field-placeholders-for-workflow-id-templating.md) and [ADR-025](0025-fingerprint-placeholder-and-nested-body-paths.md).

**Related features:** [`workflow-id-templating.feature`](../../features/workflow-id-templating.feature), [`request-validation.feature`](../../features/request-validation.feature)

## Context

Request bodies were decoded with `encoding/json`'s defaults, so every
number became a `float64`, and a body value in a workflowId template was
rendered with `fmt.Sprint`. In practice:

- `{"orderId": 10000000}` rendered `order-{body.orderId}` as `order-1e+07`.
- `{"orderId": 12345678901234567}` rendered as
  `order-1.2345678901234568e+16`. Every integer above 2⁵³ is rounded, so
  two different orders could map to the **same workflow ID**, which
  Temporal then deduplicates.
- The same rounding hit workflow **inputs**: the body passed to
  Temporal carried the rounded value.
- An object field rendered in Go syntax (`map[a:1]`).

## Decision

- The gateway decodes bodies with `json.Decoder.UseNumber()`
  (`internal/gateway.decodeBody`). Numbers stay `json.Number`, the exact
  digits the caller sent.
- A number in a workflowId renders as those exact digits. An object or
  array renders as its compact JSON (sorted keys).
- The body reaches Temporal with its numbers unchanged
  (`json.Number` marshals as its literal).
- `{fingerprint(...)}` converts numbers to float64 before hashing, so every
  fingerprint is identical to what ADR-025 defined, and the ADR-025
  caveat about integers above 2⁵³ still applies to fingerprints only.
- Request validation decodes numbers as `json.Number` too (kin-openapi,
  [ADR-029](0029-validate-requests-with-kin-openapi.md)), so `integer`
  accepts values beyond 2⁵³.
- A body with anything after its JSON value (`{"a":1} junk`, or two
  documents) is rejected with 422, like any other malformed JSON.

## Consequences

- **Workflow IDs change across this deploy** for templates that embed a
  number which used to render in exponent form or rounded (roughly: big
  integers, and integers with many trailing zeros such as `10000000`), or
  an object/array field. A retried request that straddles the deploy
  won't deduplicate against its pre-deploy run. Ordinary integers
  (`1234`) render the same as before.
- Workflows may now receive integers larger than 2⁵³ intact. A workflow
  that decodes into `float64` still rounds them, but that is now the
  workflow's choice rather than the gateway's.
