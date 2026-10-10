# ADR-020: Unresolved workflowId placeholders are rejected, not left literal

**Status:** Accepted — supersedes the "left untouched" rule of [ADR-004](0004-origin-field-placeholders-for-workflow-id-templating.md)

**Related features:** [`workflow-id-templating.feature`](../../features/workflow-id-templating.feature)

## Context

ADR-004 rendered a placeholder that couldn't be resolved as its literal
text. A request without `orderId` against `order-{body.orderId}` then
dispatched to the workflow literally named `order-{body.orderId}`. Every
such request shared that one ID, so it could start, attach to, signal, or
**terminate** a workflow that belongs to other callers. A typo'd template
(`{paht.id}`) shipped silently and failed the same way on every request.

## Decision

- **At startup** (`internal/spec/validation.go` via `internal/templating`, part of spec validation per
  [ADR-008](0008-fail-fast-validation-at-startup.md)): each placeholder
  must be `{uuidv7}` or `{origin.field}`. The origin must be one of
  `path|body|query|header` (case-insensitive) and the field must be
  non-empty. A `{path.X}` must name one of the route's own path parameters.
- **At request time** (`internal/gateway`): every trigger's workflowId is
  rendered *before* any trigger is dispatched. If any placeholder is
  unresolved, the request gets **422** `INVALID_REQUEST` naming the missing
  placeholders, and **nothing** is dispatched. That includes the other
  triggers of a multi-trigger operation.

## Consequences

- A spec with an impossible placeholder no longer loads. Fix the template.
- Body, query and header fields stay request-dependent, so they can only be
  checked per request. Declaring them `required` in the request schema
  gives a clearer 422 from body validation first.
