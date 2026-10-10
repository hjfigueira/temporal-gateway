# ADR-013: OpenTelemetry tracing, non-blocking by construction

**Status:** Accepted

**Related features:** [`observability-tracing.feature`](../../features/observability-tracing.feature)

## Context

Tracing a request through the gateway and into the Temporal workflow it
dispatches is valuable for debugging distributed latency, but a gateway's
startup (and request path) must not depend on a tracing collector being
reachable.

## Decision

`internal/telemetry.Setup` configures a process-global `TracerProvider`
exporting via OTLP/gRPC; `internal/gateway/dispatch_handler.go` starts one
span per HTTP request, and
`go.temporal.io/sdk/contrib/opentelemetry`'s interceptor (wired in
`internal/temporal/client.go`'s `NewClient`) propagates that span's trace
context into the Temporal request's headers, so the dispatched workflow's
own instrumentation (if any) continues the same trace. The gRPC dial to the
collector is non-blocking — a missing collector means spans fail to
export, not that the gateway fails to start. When `otel.enabled` is `false`
(or unset — note `config.yml`'s `${OTEL_ENABLED:-true}` default), `Setup`
leaves the global no-op provider in place, so every `otel.Tracer(...)` call
elsewhere in the code is a free no-op without any conditional logic at the
call site.

## Consequences

- Tracing code never needs an `if enabled` check outside
  `telemetry.Setup` itself — the no-op provider makes that redundant
  everywhere else.
- A request's logs are enriched with `trace_id`/`span_id` only when the
  span actually has a valid `SpanContext` (i.e. tracing is enabled), so
  disabling tracing cleanly drops those fields from logs too.
