# ADR-001: Spec-driven architecture — no handler code

**Status:** Accepted

**Related features:** [`spec-driven-routing.feature`](../features/spec-driven-routing.feature)

## Context

A gateway fronting Temporal needs an HTTP surface (routes, request
validation, response shapes) that's usually hand-written per endpoint,
which means every new route is a code change and a redeploy.

## Decision

The entire HTTP surface is generated at startup from a single OpenAPI 3.0
document (`api-spec.yaml`). Each operation carries a vendor extension,
`x-temporal`, describing what it does against Temporal. There is no
per-route handler code anywhere in the gateway — `internal/gateway`
contains exactly one generic handler, parameterized by the operation it
was built for.

## Consequences

- Adding/changing an endpoint is a YAML change, not a Go change.
- The spec is the single source of truth for routing, validation, and
  Temporal dispatch — see
  [ADR-002](0002-triggers-list-and-returnstrategy-for-multi-workflow-fan-out.md).
- Anything the spec can't express (custom business logic beyond "map this
  HTTP call onto these Temporal actions") is out of scope for the gateway
  by design — that logic belongs in the workflow itself.
- Spec errors must be caught at load time (see
  [ADR-008](0008-fail-fast-validation-at-startup.md)), since there's no
  per-route code review to catch them otherwise.
