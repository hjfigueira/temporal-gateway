# ADR-029: Request body schemas are checked at load

**Status:** Accepted. Extends [ADR-008](0008-fail-fast-validation-at-startup.md) and [ADR-009](0009-json-schema-lite-validator-with-laravel-style-messages.md).

**Related features:** [`request-validation.feature`](../../features/request-validation.feature)

## Context

The validator accepted anything for a `type` it didn't recognize, so that
a typo wouldn't reject every request. As a result, a typo such as
`type: strnig` silently turned that field's type check off. An invalid
`pattern` was skipped the same way, and every valid one was recompiled on
every request.

## Decision

- `spec.Load` runs `validate.CheckSchema` on every operation's
  `application/json` request schema, recursing into `properties` and
  `items`. An unknown `type` or a `pattern` that doesn't compile fails
  startup, joined with every other spec error (ADR-008).
- Compiled patterns are cached in `internal/validate`, so a request never
  recompiles one.
- `minLength`/`maxLength` count characters (runes), not bytes, as JSON
  Schema specifies.

## Consequences

- Keywords outside the supported subset (`oneOf`, `$ref`, `format`, ...)
  are still silently ignored (ADR-009). This ADR only rejects bad *values*
  of keywords the validator does implement.
- A spec that loaded before can now fail at startup if it has a typo'd
  `type` or a broken `pattern`. That is the intent.
- Strings with non-ASCII characters now pass length limits they used to
  fail (`"José"` is 4 characters, not 5 bytes).
