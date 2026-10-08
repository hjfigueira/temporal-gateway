# ADR-010: Response envelope separates gateway status from HTTP status

**Status:** Accepted

**Related features:** [`response-and-server-hardening.feature`](../features/response-and-server-hardening.feature)

## Context

A single HTTP status code can't express "2 of 3 triggers succeeded" in a
multi-trigger response, and a Go `nil` error alone can't express
"succeeded, but not in the way you'd expect" (see
[ADR-005](0005-accurate-start-semantics.md)).

## Decision

Every response the gateway writes embeds a shared
`response.Envelope{Status, Message}` — a machine-readable `Status` string
(`STARTED`, `WORKFLOW_RUNNING`, `VALIDATION_FAILED`, ...) one level below
the HTTP status, with `Status.IsError()` as the single predicate for "did
this actually do what was asked". `BatchResult` carries one top-level
`Status` (`WORKFLOW_STARTED` / `WORKFLOW_PARTIALLY_STARTED` /
`WORKFLOW_NOT_STARTED`) plus each trigger's own per-item result in
`Results`, so a caller can always find out exactly which trigger(s) did
what, independent of which single HTTP status code got chosen to summarize
the batch (see
[ADR-002](0002-triggers-list-and-returnstrategy-for-multi-workflow-fan-out.md)).

## Consequences

- Every response type in `internal/response` must embed `Envelope` (Go's
  embedding promotes `GetStatus()`), so `dispatchHandler` can read the
  outcome of an opaque `any` result via a one-method interface
  (`statusGetter`) instead of a type switch over every concrete type.
- `queryWorkflow`/`getResult` are the one exception: their result is
  caller-defined workflow business data, returned as-is, not wrapped in an
  `Envelope` — wrapping would corrupt the shape the caller's workflow
  actually produced.
