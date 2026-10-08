# ADR-012: Multi-file API spec merge, operation-granularity override

**Status:** Accepted

**Related features:** [`environment-config.feature`](../features/environment-config.feature)

## Context

A large API surface benefits from being split across files (e.g. a stable
base plus environment-specific overrides), but merging two OpenAPI
documents field-by-field is its own source of subtle bugs (which parts of
operation A "win" when two files both partially define it?).

## Decision

`config.yml`'s `apiSpec` accepts either a single path or a list of paths.
`spec.Load` loads each in order and merges them via `Spec.merge`: top-level
`Info` fields override field-by-field when non-empty, but each `(path,
method)` operation is an atomic unit — a later file declaring `POST
/orders` **entirely replaces** an earlier file's definition of that same
operation, rather than the two being combined field by field. Operations
that only appear in one file are unaffected.

## Consequences

- There is no partial-override facility for "same operation, just change
  one field" — an overrides file redefining an operation must restate it
  in full.
- This keeps the merge semantics easy to reason about: for any given
  route, exactly one file's definition of it is what's actually running,
  and it's always the last one in the list that defines it.
