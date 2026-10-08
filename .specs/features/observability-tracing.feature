# See .specs/adr/0013-opentelemetry-tracing-non-blocking.md
# Code: internal/telemetry/telemetry.go, internal/temporal/client.go,
#       internal/gateway/dispatch_handler.go

Feature: OpenTelemetry tracing
  When enabled, the gateway starts one span per HTTP request and exports it
  via OTLP/gRPC. That span's trace context is propagated into the Temporal
  headers of every action the request dispatches, so a workflow's own
  tracing (if instrumented) continues the same trace. A missing/unreachable
  collector must never prevent the gateway from starting or serving
  requests.

  Background:
    Given otel.enabled, otel.endpoint, otel.serviceName are configured

  Scenario: Each HTTP request gets exactly one root span
    Given otel.enabled is true
    When a request is dispatched through dispatchHandler
    Then one span is started, named after the operation's operationId
    And the span carries http.method, http.route, and operation_id attributes

  Scenario: Trace context propagates into the dispatched Temporal call
    Given otel.enabled is true and a request's span is active
    When the request dispatches a startWorkflow (or any) action
    Then the Temporal SDK's tracing interceptor reads the active span
    And propagates its trace context into the outgoing Temporal request's headers
    So a workflow instrumented with the same tracing continues the same trace

  Scenario: Request logs are enriched with trace/span IDs when tracing is active
    Given otel.enabled is true and the request's span has a valid SpanContext
    When the request is logged
    Then the log includes "trace_id" and "span_id" fields

  Scenario: Disabling tracing makes every span a free no-op
    Given otel.enabled is false
    When telemetry.Setup runs
    Then the global no-op TracerProvider is left in place
    And every otel.Tracer(...).Start() call elsewhere in the code costs
      effectively nothing and produces an invalid SpanContext
    And request logs are not enriched with trace_id/span_id

  Scenario: An unreachable OTLP collector does not block gateway startup
    Given otel.endpoint points at a collector that is not actually reachable
    When the gateway starts
    Then startup succeeds and the HTTP server begins serving requests
    But spans fail to export in the background
    # The gRPC connection to the collector is non-blocking by construction.

  Scenario: sampleRatio <= 0 defaults to sampling everything
    Given otel.sampleRatio is 0 or negative
    When telemetry.Setup configures the sampler
    Then the effective sample ratio is 1 (sample every trace)

  Scenario: Multiple bindings in one request produce bindings_dispatched/succeeded attributes
    Given a multi-trigger operation with 2 triggers, 1 of which fails
    When the request's span attributes are set
    Then temporal_gateway.bindings_dispatched is 2
    And temporal_gateway.bindings_succeeded is 1
    And the span status is set to Error because not all bindings succeeded
