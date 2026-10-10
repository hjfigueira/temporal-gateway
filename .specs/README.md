# `.specs/` — design decisions and feature specs

This directory is the project's source of truth for *why* temporal-gateway
is built the way it is, and *what* each feature is supposed to do.

- **[`adr/`](adr)** — every significant architecture decision, one file per
  decision, split into `adr/active/` (in force) and `adr/deprecated/`
  (wholly superseded, kept for history), indexed in
  [`adr/README.md`](adr/README.md). Context, decision, consequences. Add a
  new numbered file per decision; mark a superseded one rather than
  deleting it.
- **[`features/`](features)** — one Gherkin `.feature` file per feature
  area, describing expected behavior as concrete scenarios. Each file
  starts with a comment pointing back at the relevant `adr/*.md` files and
  the primary Go source files that implement it.

| Feature file | Covers |
|---|---|
| `spec-driven-routing.feature` | Routes generated from `api-spec.yaml`, no handler code |
| `multi-trigger-dispatch.feature` | `x-temporal.triggers` list, `returnStrategy`, concurrent dispatch, 207/409 |
| `multi-namespace-temporal.feature` | `temporal.connections`, per-trigger namespace, startup validation, `temporal.reconnect` |
| `workflow-id-templating.feature` | `{origin.field}` / `{uuidv7}` placeholders |
| `request-validation.feature` | Request validation from the OpenAPI spec (kin-openapi): parameters, body, field-level errors |
| `start-workflow-semantics.feature` | `startWorkflow` options, started-vs-attached, ID reuse/conflict, search attributes |
| `other-temporal-actions.feature` | `signalWorkflow`, `queryWorkflow`, `cancelWorkflow`, `terminateWorkflow`, `getResult` |
| `observability-tracing.feature` | OpenTelemetry spans and trace propagation into Temporal |
| `environment-config.feature` | `${VAR}` expansion, `.env`, multi-file spec merging, `--dry-run` |
| `response-and-server-hardening.feature` | Response envelope/status model, HTTP timeouts, body limit, request deadline, graceful shutdown |
| `health-probes.feature` | `/livez` and `/readyz` on a separate health port, readiness tied to Temporal |
| `deployment-cicd.feature` | Docker image, GHCR release publishing, CI checks |

## Schemas

[`schemas/`](schemas) holds JSON Schemas (draft 2020-12) for the two files
an operator writes. Every field carries a description, its default, and the
ADR behind it.

| Schema | Describes |
|---|---|
| [`config.schema.json`](schemas/config.schema.json) | `config.yml`: `server`, `temporal`, `apiSpec`, `health`, `otel`, `auth`, `middlewares`. Non-string fields also accept a `${VAR}` reference, so it validates the file before expansion. |
| [`x-temporal.schema.json`](schemas/x-temporal.schema.json) | One operation's `x-temporal` object in `api-spec.yaml`: `returnStrategy`, `triggers`, and every trigger option. |

They are stricter than the gateway in one way: unknown keys are rejected
(the gateway ignores them), to catch typos. Two checks are left to the
code because JSON Schema can't express them: unique `temporal.connections`
namespaces, and `health.port` differing from `server.port`. Startup
checks that need both files (a trigger's namespace and task queue against
`config.yml`) and `workflowId` placeholder syntax are also code-only.

`schemas_test.go` keeps them in step with the code: `config.yml`,
`api-spec.yaml` and the `tests/` fixtures must pass, and for each rule the
loaders enforce, the schema and `config.Load` / `spec.Load` must agree.

To validate `config.yml` in an editor using the YAML language server, add
`# yaml-language-server: $schema=.specs/schemas/config.schema.json` at the
top of the file.

## How to use this when adding or changing a feature

1. **Read first.** Before adding or modifying a feature, check `adr/` (via
   [`adr/README.md`](adr/README.md)'s index) for a relevant decision and the
   matching `features/*.feature` file for the expected behavior. Don't
   re-derive a design the project already settled — follow it, or
   deliberately revise it (see below).
2. **Keep the spec and the code in sync.** A behavior change that isn't
   reflected in the matching `.feature` file isn't done — update the
   scenarios (add, remove, or edit them) in the same change as the code.
3. **Record new or changed decisions.** A new architectural choice, or one
   that changes an existing decision's reasoning, gets a new
   `adr/active/000N-slug.md` file (see [`adr/README.md`](adr/README.md) for the
   template and the next number) plus a row in that file's index table. If
   it supersedes an earlier decision, add `**Superseded by
   [ADR-000N](000N-slug.md)**` to the top of that earlier file rather than
   deleting it.
4. **New feature area → new file.** A feature that doesn't fit an existing
   `.feature` file gets its own, following the existing format (a header
   comment linking to the relevant `adr/*.md` files + source files, then
   `Feature:` / `Scenario:` blocks), and an entry added to the table above.
5. **Changed a `config.yml` or `x-temporal` field?** Update
   [`schemas/`](schemas) and the agreement cases in `schemas_test.go`.
6. **This is documentation, not executable tests.** Nothing here runs `go
   test` — these are specifications for humans and agents to read before
   changing behavior. Go tests under `internal/*/*_test.go` remain the
   actual correctness check; a `.feature` file describes *intent*, the Go
   tests verify it.

See the repo-root `CLAUDE.md` for the standing instruction every agent
working in this repo is expected to follow.
