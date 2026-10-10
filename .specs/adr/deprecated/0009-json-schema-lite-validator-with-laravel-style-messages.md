# ADR-009: Hand-rolled JSON-Schema-lite validator with Laravel-style messages

**Superseded by [ADR-029](../active/0029-validate-requests-with-kin-openapi.md)**: requests are validated by kin-openapi straight from the spec; `internal/validate` is gone.

**Status:** Deprecated

**Related features:** [`request-validation.feature`](../../features/request-validation.feature)

## Context

`requestBody` schemas in the spec are JSON Schema, but pulling in a full
JSON Schema validation library is more generality than the gateway's use
case needs, and most such libraries produce machine-oriented error output,
not messages fit to hand back to an API caller.

## Decision

`internal/validate` implements a deliberately partial subset of JSON
Schema — `type`, `required`, `properties`, `additionalProperties`, `items`,
`enum`, `minLength`/`maxLength`, `minimum`/`maximum`, `pattern` — via a
recursive walk (`walk` in `validate.go`) that collects **every** violation
rather than stopping at the first, keyed by field path. Messages are
phrased in the style of Laravel's default validation messages ("The
`orderId` field is required.") rather than terse schema-keyword dumps.

## Consequences

- Schema keywords outside this subset (`oneOf`, `$ref`, `format`,
  conditional schemas, etc.) are silently ignored, not rejected — a spec
  author relying on one won't get an error telling them it's unsupported.
- A type mismatch on a node short-circuits further checks on that same
  node (checking `minLength` against a non-string would just add noise),
  but sibling fields are still all checked — a bad payload gets one report
  covering every actual problem, not one problem per request.
- The validator has no dependency on an external schema library, keeping
  `go.mod` and the binary small.
