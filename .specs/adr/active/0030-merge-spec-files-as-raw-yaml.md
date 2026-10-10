# ADR-030: Spec files are merged as raw YAML before parsing

**Status:** Accepted. Supersedes the merge mechanism of [ADR-012](0012-multi-file-api-spec-merge.md) (its operation-granularity rule stands) and the per-file `$ref` resolution of [ADR-029](0029-validate-requests-with-kin-openapi.md).

**Related features:** [`environment-config.feature`](../../features/environment-config.feature)

## Context

ADR-029 parsed every spec file twice, once into the gateway's own structs
and once with kin-openapi, and merged each pair with its own code
(`Spec.merge`, plus a loop over kin's paths). kin resolved each file's
`$ref`s within that file alone, so an overrides file couldn't reference a
component defined in the base file: `$ref: "#/components/schemas/Order"`
failed startup with `map key "components" not found`. Before ADR-029 that
same layout loaded (the `$ref` was simply never resolved).

## Decision

- `spec.Load` decodes each file (after `${VAR}` expansion) into a plain
  YAML map and merges them in order with one function, `mergeDoc`:
  - each operation (`paths.<path>.<method>`) and each path-level field
    replaces the earlier one whole (ADR-012);
  - each component (`components.<section>.<name>`) replaces the earlier one
    whole, and components from all files are kept;
  - each `info` field replaces the earlier one;
  - any other top-level key (`openapi`, `servers`, `security`, ...) is
    replaced outright.
- The single merged document is decoded twice from the same bytes: by
  kin-openapi for everything OpenAPI describes, and into `spec.Spec` for
  operation ids and `x-temporal`. `x-temporal` is decoded as YAML rather
  than taken from kin's extension map, because kin converts through JSON and
  would turn integers in `memo` or search attributes into float64
  (ADR-028).
- `$ref`s to other files (`./schemas.yaml#/Order`) are rejected at load:
  the merged document has no single location to resolve them against.

## Consequences

- An overrides file may `$ref` any component from any spec file, and may
  add or replace individual components.
- One merge implementation instead of two, so the two views of the spec
  can't disagree.
- An `info` field a later file sets explicitly to `""` now overrides the
  earlier value (before, empty values were skipped).
- A type error inside `x-temporal` (e.g. `triggers` written as a map) is
  reported against the merged document, without naming the file it came
  from. YAML syntax errors still name their file.
