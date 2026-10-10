# See .specs/adr/active/0001-spec-driven-architecture-no-handler-code.md
# Code: internal/app/server.go (API), internal/spec/spec.go, internal/spec/route.go,
#       internal/gateway/gateway.go

Feature: Spec-driven routing
  The gateway's entire HTTP surface is generated from api-spec.yaml at
  startup. There is no per-route handler code: one generic handler
  (internal/gateway/dispatch_handler.go) is parameterized per route from the
  loaded spec. Adding, changing, or removing an endpoint is a YAML change,
  never a Go change.

  Background:
    Given a gateway config.yml pointing at one or more api-spec.yaml files
    And each operation in the spec carries an "x-temporal" extension

  Scenario: A new path+method in the spec becomes a live route
    Given api-spec.yaml declares "POST /widgets" with a valid x-temporal binding
    When the gateway starts
    Then "POST /widgets" is registered as an HTTP route
    And no Go code was written or changed to add it

  Scenario: Route registration uses Go's http.ServeMux pattern syntax directly
    Given an OpenAPI path template like "/orders/{orderId}"
    When the route is registered
    Then the pattern "GET /orders/{orderId}" is handed to http.ServeMux as-is
    And "{orderId}" resolves as an http.ServeMux path wildcard at request time

  Scenario: Only GET/POST/PUT/PATCH/DELETE are modeled
    Given a PathItem with get/post/put/patch/delete entries
    When routes are flattened for registration
    Then OPTIONS, HEAD, and TRACE are not generated as routes
    And are not supported even if present under those keys in the YAML

  Scenario: Routes are flattened in a stable, sorted order
    Given a spec with multiple paths and methods
    When Spec.Routes() is called
    Then the result is sorted by path, then by method
    # So startup logging and route enumeration are deterministic across runs

  Scenario: An operation missing x-temporal.triggers fails spec validation
    Given an operation with no "x-temporal.triggers" entries
    When the spec is loaded
    Then spec loading fails before the HTTP server starts
    And the gateway process exits non-zero rather than serving a broken route
