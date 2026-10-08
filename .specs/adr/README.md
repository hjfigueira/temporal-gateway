# Architecture Decision Records — temporal-gateway

One file per decision. Each entry is a lightweight ADR: context, decision,
consequences. New decisions get a new numbered file rather than being
edited into an old one — if a later decision supersedes an earlier one,
add a **Superseded by ADR-0NN** line to the earlier file instead of
deleting it, so the history of *why* stays intact.

See [`../features/`](../features/) for the corresponding behavioral specs
(Gherkin `.feature` files, one per feature area) and
[`../README.md`](../README.md) for how the two relate and when to update
which.

| # | Decision |
|---|---|
| [0001](0001-spec-driven-architecture-no-handler-code.md) | Spec-driven architecture — no handler code |
| [0002](0002-triggers-list-and-returnstrategy-for-multi-workflow-fan-out.md) | `triggers` list + `returnStrategy` for multi-workflow fan-out |
| [0003](0003-multi-namespace-temporal-explicit-per-trigger.md) | Multi-namespace Temporal, explicit per-trigger |
| [0004](0004-origin-field-placeholders-for-workflow-id-templating.md) | `{origin.field}` placeholders for workflow ID templating |
| [0005](0005-accurate-start-semantics.md) | Accurate start semantics — distinguish "started" from "attached" |
| [0006](0006-terminateifrunning-reuse-policy-mapping.md) | `TerminateIfRunning` reuse policy mapped to non-deprecated primitives |
| [0007](0007-typed-search-attributes.md) | Typed search attributes, not the deprecated untyped map |
| [0008](0008-fail-fast-validation-at-startup.md) | Fail-fast validation at startup, not at request time |
| [0009](0009-json-schema-lite-validator-with-laravel-style-messages.md) | Hand-rolled JSON-Schema-lite validator with Laravel-style messages |
| [0010](0010-response-envelope-separates-gateway-status-from-http-status.md) | Response envelope separates gateway status from HTTP status |
| [0011](0011-var-expansion-over-raw-config-bytes.md) | `${VAR}` / `${VAR:-default}` expansion over raw config bytes, before YAML parsing |
| [0012](0012-multi-file-api-spec-merge.md) | Multi-file API spec merge, operation-granularity override |
| [0013](0013-opentelemetry-tracing-non-blocking.md) | OpenTelemetry tracing, non-blocking by construction |
| [0014](0014-http-server-hardening.md) | HTTP server hardening — explicit timeouts, graceful shutdown |
| [0015](0015-auth-and-middleware-config-not-yet-enforced.md) | Auth and middleware config are parsed and logged, not yet enforced |
| [0016](0016-distroless-non-root-container-image.md) | Distroless, non-root container image with a soft memory cap |
| [0017](0017-release-triggered-multi-arch-image-publish-to-ghcr.md) | Release-triggered, multi-arch image publish to GHCR |

## Adding a new decision

1. Pick the next number (`000N`), a short kebab-case slug, and create
   `000N-slug.md` using any existing entry as the template (Status /
   Related features / Context / Decision / Consequences).
2. Add a row to the table above.
3. Link it from the `.specs/features/*.feature` file(s) it affects, and
   update that feature file's scenarios if behavior changed.
4. If it supersedes an earlier ADR, add a line to the *top* of the earlier
   file: `**Superseded by [ADR-000N](000N-slug.md)**` — don't delete the
   old file.
