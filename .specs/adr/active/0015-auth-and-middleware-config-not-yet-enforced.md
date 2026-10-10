# ADR-015: Auth and middleware config are parsed and logged, not yet enforced

**Extended by [ADR-029](0029-validate-requests-with-kin-openapi.md)**: the API spec's own `security` schemes are likewise not enforced by request validation.

**Status:** Accepted

**Related features:** none yet — no `.feature` file covers enforcement,
since there is none to specify.

## Context

`AuthConfig` (`type: none|apiKey|jwt`) and `MiddlewareConfig`
(name/enabled/free-form config) exist in `internal/config/gateway.go` and
are logged at startup (`main.go`'s `logStartup`), but nothing in
`internal/gateway` currently reads or enforces either against an incoming
request.

## Decision

Ship the config *shape* ahead of the enforcement logic, explicitly
documented as not-yet-enforced in the struct comments, rather than
omitting the fields until enforcement exists.

## Consequences

- A `config.yml` setting `auth.type: apiKey` today has **no effect** on
  request handling — this is a known, intentional gap, not a bug. Anyone
  relying on it for actual access control must not deploy on this version
  believing otherwise.
- When enforcement is added, it should consume these existing structs
  rather than redesigning the config shape — check this ADR's "Superseded
  by" marker once that lands, and add a `.feature` file specifying the
  enforcement behavior at the same time.
