# temporal-gateway — agent instructions

A generic HTTP gateway in front of [Temporal](https://temporal.io), driven
entirely by an OpenAPI 3.0 spec (`api-spec.yaml`) plus a gateway config
(`config.yml`). See `README.md` for user-facing docs.

## Before adding or modifying any feature

**Read `.specs/` first.** It is the project's consolidated design record:

- **`.specs/adr/`** — every significant architecture decision made so far,
  one file per decision, indexed in `.specs/adr/README.md`. Decisions in
  force live in `.specs/adr/active/`; wholly superseded ones move to
  `.specs/adr/deprecated/` (history only — don't build on them). Check it before
  changing behavior that touches routing, dispatch, validation, config
  loading, Temporal semantics, observability, or deployment — the decision
  (and the reasoning behind it) is probably already recorded there.
- **`.specs/features/*.feature`** — Gherkin specs of expected behavior, one
  file per feature area (see `.specs/README.md` for the index). These
  describe *what the feature is supposed to do*, independent of the current
  implementation.

**When your change adds a feature, changes behavior, or revises a past
decision:**

1. Update (or add) the matching `.specs/features/*.feature` scenarios in
   the same change — a behavior change that isn't reflected there isn't
   finished.
2. Add a new `.specs/adr/active/000N-slug.md` file (and index row) if the change
   introduces a new design decision, or changes the reasoning behind an
   existing one. If it supersedes an earlier decision, add `**Superseded by
   ADR-000N**` to the top of that earlier file instead of deleting it —
   don't erase the history of *why*. If the supersession is total, also
   move it to `.specs/adr/deprecated/` and set its status to `Deprecated`.
3. If it's a genuinely new feature area with no existing `.feature` file,
   create one following the existing format and add it to the table in
   `.specs/README.md`.

Do not skip this because a change looks small — the ADRs exist specifically
to prevent a future change (yours or someone else's) from accidentally
reversing a deliberate decision (e.g. "why does `startWorkflow` read the
server's `Started` flag via a gRPC interceptor?", "why is `returnStrategy` per-operation
and not per-trigger?") without realizing it was deliberate.

## Project layout

```
main.go              entrypoint: loads config/spec, wires everything, serves HTTP
internal/config      config.yml parsing
internal/spec        api-spec.yaml parsing + validation
internal/gateway     HTTP handler generation, request validation, dispatch
internal/templating  workflowId placeholders: parsing, rendering, fingerprint
internal/temporal    Temporal client(s), namespace connection pool, dispatch
internal/validate    JSON Schema-lite request body validation
internal/response    response envelope + status types
internal/telemetry   OpenTelemetry setup
internal/envsubst    ${VAR}/${VAR:-default} expansion
internal/dotenv      .env file loading
```

## Development commands

```bash
go build ./...
go vet ./...
gofmt -s -l .
go test -race ./...
```
