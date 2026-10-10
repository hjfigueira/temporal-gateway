# ADR-031: A startWorkflow's task queue may come from the workflow catalog

**Status:** Accepted. Supersedes in part [ADR-008](0008-fail-fast-validation-at-startup.md) (`temporal.workflows` is no longer purely informational).

**Related features:** [`multi-namespace-temporal.feature`](../../features/multi-namespace-temporal.feature), [`start-workflow-semantics.feature`](../../features/start-workflow-semantics.feature)

## Context

`temporal.connections[].workflows` lets config.yml declare each workflow
type's default task queue, and `startWorkflow` falls back to it when a
trigger sets no `taskQueue`. The feature specs describe that fallback.
But spec validation also required `taskQueue` on every `startWorkflow`
trigger, so the fallback could never run and the catalog only fed startup
logs.

## Decision

- `spec.Load` no longer requires `taskQueue` on `startWorkflow`; the spec
  package can't see config.yml.
- `temporal.ValidateNamespaces` becomes `temporal.ValidateBindings`. At
  startup, before any dial, it checks every trigger's namespace (as
  before) and that every `startWorkflow` without its own `taskQueue` finds
  one in that namespace's catalog. It reports every problem at once.
- At dispatch, a trigger's own `taskQueue` still wins over the catalog.

## Consequences

- A spec can omit `taskQueue` and keep task queues in config.yml, per
  namespace, so one spec can target environments that name queues
  differently.
- A missing task queue still fails at startup (ADR-008), with an error
  naming the operation, trigger, namespace and workflow type.
- The catalog's `signals` and `queries` remain informational: they are
  logged at startup and never checked.
