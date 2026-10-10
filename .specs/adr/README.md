# Architecture Decision Records — temporal-gateway

One file per decision. Each entry is a lightweight ADR: context, decision,
consequences. New decisions get a new numbered file rather than being
edited into an old one — if a later decision supersedes an earlier one,
add a **Superseded by ADR-0NN** line to the earlier file instead of
deleting it, so the history of *why* stays intact.

ADRs live in one of two folders. Numbers are global and never reused.

- **[`active/`](active)** — decisions currently in force, including ones
  only *partly* superseded or extended by a later ADR (the remaining part
  still applies).
- **[`deprecated/`](deprecated)** — decisions *wholly* superseded or
  withdrawn. Kept for the history of why; don't build on them.

See [`../features/`](../features) for the corresponding behavioral specs
(Gherkin `.feature` files, one per feature area) and
[`../README.md`](../README.md) for how the two relate and when to update
which.

| # | Decision |
|---|---|
| [0001](active/0001-spec-driven-architecture-no-handler-code.md) | Spec-driven architecture — no handler code |
| [0002](active/0002-triggers-list-and-returnstrategy-for-multi-workflow-fan-out.md) | `triggers` list + `returnStrategy` for multi-workflow fan-out |
| [0003](active/0003-multi-namespace-temporal-explicit-per-trigger.md) | Multi-namespace Temporal, explicit per-trigger |
| [0004](active/0004-origin-field-placeholders-for-workflow-id-templating.md) | `{origin.field}` placeholders for workflow ID templating |
| [0005](deprecated/0005-accurate-start-semantics.md) | Accurate start semantics — distinguish "started" from "attached" |
| [0006](active/0006-terminateifrunning-reuse-policy-mapping.md) | `TerminateIfRunning` reuse policy mapped to non-deprecated primitives |
| [0007](active/0007-typed-search-attributes.md) | Typed search attributes, not the deprecated untyped map |
| [0008](active/0008-fail-fast-validation-at-startup.md) | Fail-fast validation at startup, not at request time |
| [0009](active/0009-json-schema-lite-validator-with-laravel-style-messages.md) | Hand-rolled JSON-Schema-lite validator with Laravel-style messages |
| [0010](active/0010-response-envelope-separates-gateway-status-from-http-status.md) | Response envelope separates gateway status from HTTP status |
| [0011](active/0011-var-expansion-over-raw-config-bytes.md) | `${VAR}` / `${VAR:-default}` expansion over raw config bytes, before YAML parsing |
| [0012](active/0012-multi-file-api-spec-merge.md) | Multi-file API spec merge, operation-granularity override |
| [0013](active/0013-opentelemetry-tracing-non-blocking.md) | OpenTelemetry tracing, non-blocking by construction |
| [0014](active/0014-http-server-hardening.md) | HTTP server hardening — explicit timeouts, graceful shutdown |
| [0015](active/0015-auth-and-middleware-config-not-yet-enforced.md) | Auth and middleware config are parsed and logged, not yet enforced |
| [0016](active/0016-distroless-non-root-container-image.md) | Distroless, non-root container image with a soft memory cap |
| [0017](active/0017-release-triggered-multi-arch-image-publish-to-ghcr.md) | Release-triggered, multi-arch image publish to GHCR |
| [0018](active/0018-retry-temporal-dial-at-startup.md) | Retry the Temporal dial at startup instead of exiting |
| [0019](active/0019-liveness-and-readiness-probes-on-a-separate-port.md) | Liveness and readiness probes on a separate port |
| [0020](active/0020-unresolved-workflow-id-placeholders-are-rejected.md) | Unresolved workflowId placeholders are rejected, not left literal |
| [0021](active/0021-started-flag-from-server-not-describe-first.md) | Read "started vs attached" from the server, not a prior describe |
| [0022](active/0022-per-request-limits-and-sanitized-errors.md) | Per-request body limit, dispatch deadline, and sanitized errors |
| [0023](active/0023-temporal-connection-options-and-concurrent-dial.md) | Concurrent namespace dial; CA bundle, server name, and API key |
| [0024](active/0024-ci-checks-on-every-push-and-pr.md) | CI runs gofmt, vet, and race tests on every push and PR |
| [0025](active/0025-fingerprint-placeholder-and-nested-body-paths.md) | `{fingerprint(...)}` placeholder and nested body paths |
| [0026](active/0026-serve-http-before-temporal-connects.md) | Serve HTTP immediately; Temporal readiness via probes |
| [0027](active/0027-godotenv-for-dotenv-loading.md) | Load `.env` with `joho/godotenv`, not a hand-rolled parser |
| [0028](active/0028-exact-json-numbers-in-request-bodies.md) | Exact JSON numbers in request bodies |
| [0029](active/0029-request-body-schemas-checked-at-load.md) | Request body schemas are checked at load |

## Adding a new decision

1. Pick the next number (`000N`, across both folders), a short kebab-case
   slug, and create `active/000N-slug.md` using any existing entry as the
   template (Status / Related features / Context / Decision / Consequences).
2. Add a row to the table above.
3. Link it from the `.specs/features/*.feature` file(s) it affects, and
   update that feature file's scenarios if behavior changed.
4. If it supersedes an earlier ADR, add a line to the *top* of the earlier
   file: `**Superseded by [ADR-000N](../active/000N-slug.md)**` — don't
   delete the old file. If the supersession is total, set its
   `**Status:**` to `Deprecated`, move it to `deprecated/`, and fix the
   links to it (this index, other ADRs, and the `# See` comments in
   `../features/*.feature`). A partial supersession stays in `active/`.
