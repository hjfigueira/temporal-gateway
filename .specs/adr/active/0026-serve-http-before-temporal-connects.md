# ADR-026: Serve HTTP immediately; Temporal readiness via probes

**Status:** Accepted. Supersedes the "API server unbound until every namespace is connected" part of [ADR-018](0018-retry-temporal-dial-at-startup.md) and [ADR-019](0019-liveness-and-readiness-probes-on-a-separate-port.md).

**Related features:** [`health-probes.feature`](../../features/health-probes.feature), [`multi-namespace-temporal.feature`](../../features/multi-namespace-temporal.feature)

## Context

The API port stayed closed until every Temporal namespace was dialed. A
caller then saw `ECONNREFUSED`, which looks like "the gateway is down"
rather than "the gateway is up, Temporal isn't". One slow namespace also
held back the routes for every healthy one. The readiness probe already
exists to tell orchestrators whether to send traffic.

## Decision

- The API server binds as soon as config and spec are loaded and
  validated. `ValidateNamespaces` (now `ValidateBindings`, ADR-031) needs only config, so it now runs
  *before* any dial.
- `temporal.NewConnections` only builds the namespace table. `Connect`
  dials every namespace concurrently in the background, with the same
  ADR-018 retry, and each namespace becomes usable the moment its own dial
  succeeds.
- A request for a namespace that isn't connected yet gets **503**
  `UNAVAILABLE`, the same answer as Temporal itself being unavailable at
  runtime (which also maps to 503 now, not 502).
- `/readyz` reports each namespace individually: `"not connected yet"`
  until it's dialed, then its live health check (ADR-019 unchanged
  otherwise).
- A dial that gives up (`temporal.reconnect.maxAttempts` reached) still
  stops the process, so the orchestrator restarts it. With the default
  `maxAttempts: 0` it retries forever.
- `--dry-run` is unchanged: it dials synchronously, once, and never serves.

## Consequences

- During startup, and during a Temporal outage, callers get a clear 503
  with the gateway envelope instead of a refused connection. Behind a load
  balancer, readiness still keeps traffic away until the pod is ready.
- Routes for a connected namespace work while another namespace is still
  dialing.
- Shutdown waits for the background dial to stop, so a client that
  connects mid-shutdown is still closed.
