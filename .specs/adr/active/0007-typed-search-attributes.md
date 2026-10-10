# ADR-007: Typed search attributes, not the deprecated untyped map

**Status:** Accepted

**Related features:** [`start-workflow-semantics.feature`](../../features/start-workflow-semantics.feature)

## Context

`client.StartWorkflowOptions.SearchAttributes` (a plain
`map[string]interface{}`) is deprecated in the Temporal Go SDK in favor of
`TypedSearchAttributes`, which requires each attribute to be built through
an explicitly typed key constructor (`SearchAttributeKeyString`,
`SearchAttributeKeyBool`, ...).

## Decision

`x-temporal.triggers[].searchAttributes` is a list of `{name, type,
value}`, not a bare map — `type` is one of `string`, `keyword`, `bool`,
`int`, `float`, `time` (RFC3339 string), or `keywordList`. The spec
validator checks `value`'s shape against the declared `type` at load time
(`internal/spec/validation.go`'s `validateSearchAttributeValue`); the
dispatcher (`internal/temporal/search_attributes.go`) then builds the
SDK's typed update from the same `{name, type, value}` triple, assuming
(since validation already passed) that the conversion cannot fail.

## Consequences

- Requires every search attribute in the spec to carry an explicit type,
  more verbose than a plain map but matching how the Temporal server
  actually keys them (name *and* type).
- A type/value mismatch is a spec-load-time error, not a dispatch-time
  surprise on the first request that hits the route.
