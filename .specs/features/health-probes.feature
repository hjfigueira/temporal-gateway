# See .specs/adr/active/0019-liveness-and-readiness-probes-on-a-separate-port.md
# See .specs/adr/active/0026-serve-http-before-temporal-connects.md
# Code: internal/health/health.go, internal/temporal/connections.go
#       (Connections.CheckHealth), internal/config/gateway.go (HealthConfig),
#       main.go (startHealthServer, serve)

Feature: Liveness and readiness probes
  The gateway serves GET /livez and GET /readyz on a separate health port.
  Liveness only says the process is up. Readiness also says every Temporal
  namespace the gateway depends on is reachable, so traffic isn't sent to a
  gateway that can only answer with errors.

  Background:
    Given health.enabled is true and health.port is 8082
    And server.port is 8081

  Scenario: Probes are served on their own port
    When the gateway starts
    Then GET /livez and GET /readyz are served on port 8082
    And neither path is served on port 8081

  Scenario: The API serves while Temporal is still connecting
    Given Temporal is not reachable
    When the gateway starts and retries the dial
    Then GET /livez returns 200 {"status":"ok"}
    And GET /readyz returns 503 with checks "default" = "not connected yet"
    And the API server on port 8081 is already serving
    And a request to a route for "default" gets 503 with status "UNAVAILABLE"

  Scenario: Each namespace becomes usable as soon as it connects
    Given "default" is connected but "notifications" is still being dialed
    When a request is made to a route targeting "default"
    Then it is dispatched normally
    But a route targeting "notifications" gets 503 with status "UNAVAILABLE"
    And GET /readyz returns 503 until "notifications" connects too

  Scenario: Readiness passes when every namespace is healthy
    Given namespaces "default" and "notifications" are connected and healthy
    When GET /readyz is requested
    Then it returns 200 with status "ready"
    And checks "default" and "notifications" are both "ok"

  Scenario: Readiness fails when any namespace is unhealthy
    Given the gateway is serving
    And the Temporal server for "notifications" becomes unreachable
    When GET /readyz is requested
    Then it returns 503 with status "not_ready"
    And checks "notifications" holds the health check error
    And GET /livez still returns 200
    # Restarting the gateway can't fix Temporal, so liveness ignores it.

  Scenario: Readiness recovers without a restart
    Given GET /readyz was failing because Temporal was unreachable
    When Temporal becomes reachable again
    Then GET /readyz returns 200 again

  Scenario: A hung Temporal fails readiness instead of hanging the probe
    Given a namespace's health check doesn't answer
    When GET /readyz is requested
    Then it returns 503 within about 2 seconds

  Scenario: Readiness fails during shutdown
    Given the gateway is serving and ready
    When it receives SIGINT or SIGTERM
    Then GET /readyz returns 503 with reason "shutting down"
    And in-flight API requests are still drained

  Scenario: Probes can be disabled
    Given health.enabled is false
    When the gateway starts
    Then no health server is started

  Scenario: Invalid health config is rejected
    Given health.enabled is true
    And health.port is out of range or equals server.port
    When the config is loaded
    Then config loading fails naming health.port

  Scenario: A taken health port fails startup
    Given another process is listening on health.port
    When the gateway starts
    Then startup fails before Temporal is dialed

  Scenario: --dry-run does not start the health server
    When the gateway runs with --dry-run
    Then no health server is started
