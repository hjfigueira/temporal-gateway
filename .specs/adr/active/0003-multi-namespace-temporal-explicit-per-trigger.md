# ADR-003: Multi-namespace Temporal, explicit per-trigger

**Status:** Accepted

**Related features:** [`multi-namespace-temporal.feature`](../../features/multi-namespace-temporal.feature)

## Context

A single gateway may need to front workflows that live in different
Temporal namespaces, or even different clusters entirely (e.g. a
core-orders namespace and a shared notifications namespace).

## Decision

`config.yml`'s `temporal.connections` is a list; the gateway dials one
client per entry at startup and keeps them in a `namespace -> *Connection`
map (`internal/temporal/connections.go`). Every `x-temporal.triggers` entry
**must** name a `namespace` explicitly — it is never inferred from the
route, a default, or the workflow type. The mapping is validated at
startup: every namespace a trigger references must have a matching
`temporal.connections` entry, or the gateway refuses to start
(`temporal.ValidateNamespaces`).

## Consequences

- Two triggers on the same operation can legitimately target different
  namespaces/clusters — this is the mechanism
  [ADR-002](0002-triggers-list-and-returnstrategy-for-multi-workflow-fan-out.md)'s
  fan-out relies on.
- There is deliberately no "default namespace" fallback — an operator
  copy-pasting a trigger into a new spec file must consciously set
  `namespace`, rather than it silently targeting the wrong cluster.
- `Connections` is built once and read-only afterward, so it's safe to
  share across the concurrent per-request dispatches without locking.
