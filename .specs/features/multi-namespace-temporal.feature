# See .specs/adr/active/0003-multi-namespace-temporal-explicit-per-trigger.md,
#     .specs/adr/active/0018-retry-temporal-dial-at-startup.md,
#     .specs/adr/active/0023-temporal-connection-options-and-concurrent-dial.md
# Code: internal/config/temporal.go, internal/temporal/connections.go,
#       internal/temporal/client.go, internal/temporal/catalog.go

Feature: Multi-namespace Temporal connections
  A single gateway process can serve routes against several Temporal
  namespaces - even different clusters - at once. config.yml's
  temporal.connections lists every namespace the gateway dials a client
  for. Every x-temporal trigger names which one it targets via its own
  required "namespace" field; there is no default or inferred namespace.

  Background:
    Given config.yml declares one or more temporal.connections entries
    And each entry has a unique, non-empty "namespace"

  Scenario: Gateway dials one client per configured namespace at startup
    Given temporal.connections lists namespaces "default" and "notifications"
    When the gateway starts
    Then a Temporal client is dialed for "default"
    And a separate Temporal client is dialed for "notifications"

  Scenario: Namespaces are dialed concurrently and every failure is reported
    Given temporal.connections lists "default", "down-a", and "down-b"
    And "down-a" and "down-b" are unreachable with maxAttempts 1
    When the gateway starts
    Then all three are dialed at the same time, not one after another
    And startup fails with an error naming both "down-a" and "down-b"
    And the client dialed for "default" is closed

  Scenario: A private CA and server name can be configured for TLS
    Given a connection with tls.enabled, tls.caPath, and tls.serverName set
    When the client is dialed
    Then the server certificate is verified against the CA bundle at caPath
    And against serverName instead of the host
    But a caPath that is missing or holds no PEM certificate fails startup without retrying

  Scenario: An API key authenticates to Temporal and implies TLS
    Given a connection with apiKey set (via "${TEMPORAL_API_KEY}")
    When the client is dialed
    Then API-key credentials are sent on every call
    And TLS is used even if tls.enabled is false
    And the key never appears in the logs

  Scenario: A TLS client keypair must be complete
    Given a connection with tls.certPath set but tls.keyPath unset
    When the config is loaded
    Then loading fails saying certPath and keyPath must be set together

  Scenario: An unreachable Temporal is retried at startup instead of exiting
    Given temporal.reconnect.interval is "5s" and maxAttempts is 0
    And the Temporal server for namespace "default" is not reachable
    When the gateway starts
    Then it logs a warning naming "default", the host, and the attempt number
    And it retries the dial every 5 seconds
    And the HTTP server is not bound while it waits
    When the Temporal server becomes reachable
    Then the next attempt connects and startup continues

  Scenario: Reconnect attempts are capped by maxAttempts
    Given temporal.reconnect.maxAttempts is 3
    And the Temporal server for namespace "default" is never reachable
    When the gateway starts
    Then it dials 3 times, then startup fails naming "default" and the attempt count

  Scenario: A shutdown signal interrupts waiting for Temporal
    Given the gateway is retrying an unreachable Temporal at startup
    When it receives SIGINT or SIGTERM
    Then it stops retrying immediately and exits without binding the HTTP server

  Scenario: reconnect defaults to retrying every 5s forever
    Given config.yml has no temporal.reconnect section
    When the gateway starts with an unreachable Temporal
    Then it retries every 5 seconds with no attempt limit

  Scenario: Malformed reconnect settings are rejected
    Given temporal.reconnect.interval is "soon", "0s", or maxAttempts is negative
    When the config is loaded
    Then config loading fails naming the bad temporal.reconnect field

  Scenario: Configuration errors are not retried
    Given a connection with tls.enabled and an unreadable certPath/keyPath
    When the gateway starts
    Then startup fails immediately without any reconnect attempts

  Scenario: Temporal going away after startup does not stop the gateway
    Given the gateway is serving with all namespaces connected
    When the Temporal server becomes unreachable
    Then the gateway keeps serving
    And requests dispatched meanwhile fail with a Temporal error
    And dispatch succeeds again once the SDK client has reconnected

  Scenario: Two triggers on the same operation target different namespaces
    Given an operation with trigger A targeting namespace "default"
    And trigger B targeting namespace "notifications"
    When the operation is dispatched
    Then trigger A is sent via the "default" namespace's client
    And trigger B is sent via the "notifications" namespace's client

  Scenario: temporal.connections requires at least one entry
    Given config.yml's temporal.connections is empty
    When the config is loaded
    Then config loading fails
    And the gateway does not start

  Scenario: Duplicate namespace names are rejected
    Given two temporal.connections entries both named "default"
    When the config is loaded
    Then config loading fails naming the duplicate namespace

  Scenario: A trigger referencing an unconfigured namespace fails at startup
    Given api-spec.yaml has a trigger with namespace "payments"
    And config.yml's temporal.connections has no entry named "payments"
    When the gateway starts
    Then startup fails before the HTTP server binds
    And the error names the operation, the trigger index, and "payments"
    # This is checked once at startup (temporal.ValidateNamespaces), not
    # rediscovered as a per-request dispatch error on first use of the route.

  Scenario: A workflow catalog entry only supplies a default task queue
    Given temporal.connections["default"].workflows includes a workflow named "OrderWorkflow" with taskQueue "orders-task-queue"
    And a startWorkflow trigger for "OrderWorkflow" does not set its own taskQueue
    When the trigger is dispatched
    Then the catalog's "orders-task-queue" is used
    But an explicit taskQueue on the trigger always takes precedence over the catalog

  Scenario: An unknown workflow/signal/query in the catalog is not cross-checked
    Given temporal.workflows documents signals/queries informationally only
    When a trigger addresses a workflow, signal, or query not listed there
    Then this is not caught at startup
    And surfaces only as an ordinary Temporal error at request time
    # Accepted gap - see ADR-008.
