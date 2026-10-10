# ADR-019: Liveness and readiness probes on a separate port

**Status:** Accepted

**Related features:** [`health-probes.feature`](../../features/health-probes.feature)

## Context

Orchestrators like Kubernetes need two different signals from the
gateway. *Liveness*: is the process wedged and in need of a restart?
*Readiness*: should it receive traffic right now? The gateway depends
strictly on Temporal, since every route dispatches to it. So a gateway
that can't reach Temporal can only answer with errors, but restarting it
won't fix Temporal.

[ADR-018](0018-retry-temporal-dial-at-startup.md) keeps the API server
unbound until every namespace is connected, which can take arbitrarily
long. Probes served by the API server would therefore fail liveness during
that wait, and the orchestrator would kill the pod while it's doing exactly
what it should.

## Decision

- A **separate probe server** (`health.host`/`health.port`, default
  `8082`, `health.enabled` default `true`) serves `GET /livez` and
  `GET /readyz` (`internal/health`). It binds **before** Temporal is
  dialed, synchronously, so a port conflict fails startup. Its own port
  also means probe paths can never collide with a route in
  `api-spec.yaml`, and probe traffic stays out of request tracing.
- **`/livez`** always returns 200 while the process can serve HTTP. It
  deliberately checks no dependencies.
- **`/readyz`** returns 503 with a `reason` until every namespace is
  connected. After that it calls `CheckHealth` on every namespace's client,
  concurrently, with a 2s budget, on every probe. It returns 200 only if
  all pass, otherwise 503 listing each namespace's result.
- On SIGINT/SIGTERM `/readyz` flips to 503 (`"shutting down"`) before the
  API server's graceful shutdown starts. The probe server is closed last.
- `--dry-run` doesn't start the probe server.

The check runs live on each probe rather than being cached by a background
poller. A Temporal health check is a cheap gRPC call, and a live check
can't report stale state.

## Consequences

- A Temporal outage after startup removes the pod from the load balancer
  instead of letting it answer requests with errors. Once the SDK
  reconnects, readiness recovers on its own, with no restart.
- Because every replica fails readiness together during a Temporal outage,
  the whole Service has no ready endpoints for that time. Clients see a
  connection error or a 503 from the load balancer instead of the
  gateway's FAILED envelope. This is the intended trade-off.
- One more port to expose (`EXPOSE 8081 8082` in the Dockerfile).
- `/readyz` returns raw Temporal error strings. That's fine on an internal
  probe port, but `health.port` shouldn't be exposed publicly.
