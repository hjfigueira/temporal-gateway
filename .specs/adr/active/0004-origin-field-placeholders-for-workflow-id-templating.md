# ADR-004: `{origin.field}` placeholders for workflow ID templating

**Superseded in part by [ADR-020](0020-unresolved-workflow-id-placeholders-are-rejected.md)** — unresolved placeholders are now rejected (422), not left literal.

**Status:** Accepted

**Related features:** [`workflow-id-templating.feature`](../../features/workflow-id-templating.feature)

## Context

A workflow ID almost always needs to be derived from the incoming request
(an order ID, a customer ID, a generated correlation ID), not hardcoded or
left to the caller to compute.

## Decision

Any `x-temporal` string field (chiefly `workflowId`) may embed
`{origin.field}` placeholders, resolved at request time: `path`, `body`,
`query`, `header`, plus the reserved `{uuidv7}` (no origin) for a freshly
generated UUIDv7. Rendering is pure string substitution
(`internal/gateway/template.go`'s `renderTemplate`) against a resolver
closure built fresh per request; a placeholder the resolver can't satisfy is
left untouched rather than erroring, failing visibly in the resulting
workflow ID rather than failing the whole request.

## Consequences

- Templating is confined to a small, explicit set of origins — there's no
  general expression language, arithmetic, or string manipulation, keeping
  the attack surface and the mental model small.
- A body field used in a template is stringified via `fmt.Sprint` for
  non-string JSON values (objects/arrays render as Go-syntax) rather than
  being rejected — permissive by design, since workflow IDs are
  human-debugging aids more than strictly-typed identifiers.
