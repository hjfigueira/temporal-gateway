# ADR-018: Retry the Temporal dial at startup instead of exiting

**Status:** Accepted

**Related features:**
[`multi-namespace-temporal.feature`](../../features/multi-namespace-temporal.feature),
[`environment-config.feature`](../../features/environment-config.feature)

## Context

The gateway dials one Temporal client per `temporal.connections` entry at
startup ([ADR-003](0003-multi-namespace-temporal-explicit-per-trigger.md)).
Before this decision, a single failed dial made `run` return an error and
the process exit 1 - consistent with
[ADR-008](0008-fail-fast-validation-at-startup.md)'s fail-fast startup.
But an unreachable Temporal is usually a *transient* condition, not a
config mistake: the gateway and Temporal started together (docker compose,
a Kubernetes rollout), Temporal restarting, a brief network blip. Exiting
just pushes the retry onto the orchestrator's restart policy (crash loops,
growing back-off, noisy alerts) or, with no orchestrator, leaves the
gateway down.

## Decision

`temporal.reconnect` configures a retry of the **dial only**:

- `interval` (Go duration, default `5s`) is waited between attempts.
- `maxAttempts` caps attempts per namespace; `0` (default) means retry
  until connected.
- Each failed attempt is logged at WARN with the namespace, host, attempt
  number, and error.
- The wait is interruptible: SIGINT/SIGTERM cancels the startup context
  (now created at the top of `run` and shared with `serve`), so a gateway
  stuck waiting for Temporal still stops promptly.

Things that are *not* retried, because no amount of waiting fixes them:
building client options (tracing interceptor, unreadable TLS keypair),
config/spec validation, and `ValidateNamespaces`. Those still fail fast
per ADR-008.

`--dry-run` always forces `maxAttempts: 1`: it's a pass/fail check for CI
or pre-deploy, and hanging forever on an unreachable Temporal would defeat
it.

The HTTP server is still bound only after **every** namespace is connected
- a gateway that's listening is one that can dispatch. Serving early and
returning 503 for not-yet-connected namespaces was considered, but would
make `Connections` mutable after startup (breaking ADR-003's
"read-only, no locking" property) for little gain: a load balancer or
readiness probe already treats a not-yet-listening gateway as not ready.

After startup no extra mechanism is needed: the SDK's gRPC connection
reconnects by itself, and a request made while Temporal is down fails with
an ordinary Temporal error rather than crashing the process.

## Consequences

- Startup order between the gateway and Temporal no longer matters.
- The probe server binds before the dial, so liveness passes while the
  gateway waits and readiness reports `"waiting for temporal"` - see
  [ADR-019](0019-liveness-and-readiness-probes-on-a-separate-port.md).
- With the default `maxAttempts: 0`, a permanently wrong `host` makes the
  gateway wait forever (logging a WARN every `interval`) instead of exiting.
  Operators who prefer the old crash-and-let-the-orchestrator-restart
  behavior can set `maxAttempts: 1`.
- Namespaces are dialed sequentially, so the second namespace isn't
  attempted until the first one connects.
- This partially relaxes ADR-008 for the one startup stage that depends on
  an external service's availability; ADR-008 still holds for everything
  that's a property of the config itself.
