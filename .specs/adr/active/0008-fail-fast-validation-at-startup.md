# ADR-008: Fail-fast validation at startup, not at request time

**Status:** Accepted

**Related features:** every `.feature` file touches this to some degree;
see especially
[`multi-namespace-temporal.feature`](../../features/multi-namespace-temporal.feature)
and
[`environment-config.feature`](../../features/environment-config.feature).

## Context

A gateway whose entire behavior comes from two config files (`config.yml`,
`api-spec.yaml`) is especially exposed to config typos — unknown enum
values, malformed durations, a trigger referencing an unconfigured
namespace. Discovering these on the first request that exercises the
broken route is a bad failure mode in production.

## Decision

Everything that *can* be checked without a live request is checked at
startup, before the HTTP server binds: `config.Load` validates
`temporal.connections` (non-empty, unique namespaces) and `apiSpec` paths;
`spec.Load` validates every `x-temporal` binding (required fields per
action, duration strings, enum values, search-attribute type/value match)
and joins every problem found into one error rather than stopping at the
first; `temporal.ValidateNamespaces` cross-checks every trigger's namespace
against the dialed connections. A `--dry-run` flag runs this entire startup
sequence (load, parse, validate, dial Temporal) and exits 0/1 without
starting the server, for use in CI or a pre-deploy check.

## Consequences

- `main.go`'s `run` is a straight-line sequence of fallible stages, each
  returning an error immediately rather than the gateway limping into a
  half-valid running state.
- Validation errors are collected and joined (`errors.Join`), so a spec
  with five mistakes reports all five in one run, not one per redeploy.
- What *cannot* be checked at startup (e.g. an unknown workflow/signal/query
  name, since `temporal.workflows` is informational only — see
  [ADR-003](0003-multi-namespace-temporal-explicit-per-trigger.md)) still
  surfaces as an ordinary Temporal error at request time; this is an
  accepted gap, not an oversight.
- An *unreachable* Temporal at startup is not treated as a validation
  failure: the dial is retried per `temporal.reconnect` (except under
  `--dry-run`) - see
  [ADR-018](0018-retry-temporal-dial-at-startup.md).
