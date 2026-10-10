# ADR-014: HTTP server hardening — explicit timeouts, graceful shutdown

**Extended by [ADR-022](0022-per-request-limits-and-sanitized-errors.md)** — `WriteTimeout` is now derived from `server.requestTimeout` (+5s).

**Status:** Accepted

**Related features:** [`response-and-server-hardening.feature`](../../features/response-and-server-hardening.feature)

## Context

Go's `net/http` defaults have no read-header timeout, which exposes a
server to slow-client ("slowloris") resource exhaustion, and no default
graceful-shutdown wiring.

## Decision

`internal/app`'s `newServer` sets explicit `ReadHeaderTimeout` (10s),
`ReadTimeout`/`WriteTimeout` (30s), and `IdleTimeout` (60s). `serve` listens
for `SIGINT`/`SIGTERM` and calls `server.Shutdown` with a bounded
`shutdownTimeout` (10s), giving in-flight requests a chance to finish
instead of being dropped; the same timeout bounds telemetry flush on the
same shutdown path.

## Consequences

- A request that's still dispatching to Temporal when shutdown begins has
  up to 10s to finish before the connection is forced closed — callers
  making long-running `getResult` calls should be aware shutdown doesn't
  wait indefinitely.
- Every fallible setup stage (an `internal/app` module's `Init`) returns an error rather than
  calling `os.Exit` directly, so deferred cleanup (closing Temporal
  connections, flushing telemetry) always runs; `os.Exit` is called
  exactly once, in `main`, after `app.Run` has already unwound (ADR-032).
