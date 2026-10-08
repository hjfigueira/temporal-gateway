# See .specs/adr/0003-multi-namespace-temporal-explicit-per-trigger.md
# Code: internal/config/temporal.go, internal/temporal/connections.go,
#       internal/temporal/catalog.go

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
    Given temporal.connections["default"].workflows includes
      a workflow named "OrderWorkflow" with taskQueue "orders-task-queue"
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
