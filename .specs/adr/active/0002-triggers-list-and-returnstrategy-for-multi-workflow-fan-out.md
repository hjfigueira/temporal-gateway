# ADR-002: `triggers` list + `returnStrategy` for multi-workflow fan-out

**Status:** Accepted

**Related features:** [`multi-trigger-dispatch.feature`](../../features/multi-trigger-dispatch.feature)

## Context

The first cut of `x-temporal` modeled one action per operation. Real use
cases (e.g. "create an order and kick off a notification") need one HTTP
call to affect more than one workflow, possibly in different namespaces,
and the caller needs to know if some succeeded and others didn't.

## Decision

`x-temporal.triggers` is a list, not a single object. Every trigger in the
list is dispatched concurrently (`internal/gateway/dispatch_handler.go`'s
`dispatchAll`). A sibling `x-temporal.returnStrategy` (`acceptPartial`,
default, or `allOrNothing`) governs how the triggers' individual outcomes
combine into **one** HTTP response when there's more than one trigger:

- `acceptPartial`: a genuine mix of outcomes is `207 Multi-Status`; a
  uniform outcome (all or none) gets an ordinary single status.
- `allOrNothing`: anything short of every trigger succeeding is a single
  `409 Conflict`.

A single-trigger operation always reports that trigger's own outcome
directly — `returnStrategy` only matters once there's more than one.

## Consequences

- Every trigger still requires its own `namespace` and `workflowId`, even
  when there's only one — the per-trigger shape never special-cases the
  single-trigger case.
- The response envelope needs a status vocabulary one level below HTTP
  status codes (see
  [ADR-010](0010-response-envelope-separates-gateway-status-from-http-status.md))
  to express "2 of 3 succeeded" in one JSON body.
- One slow/failing trigger can't block the others — they're dispatched in
  parallel and a failure in one doesn't cancel the rest.
