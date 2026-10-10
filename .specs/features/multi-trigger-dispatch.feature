# See .specs/adr/active/0002-triggers-list-and-returnstrategy-for-multi-workflow-fan-out.md
# Code: internal/gateway/dispatch_handler.go, internal/spec/spec.go
#       (TemporalSpec, ReturnStrategy), internal/response/response.go
#       (BatchResult)

Feature: Multi-trigger dispatch and returnStrategy
  An operation's "x-temporal.triggers" is a list. Every trigger dispatches
  concurrently against Temporal. A single-trigger operation always reports
  that trigger's own outcome directly. A multi-trigger operation's overall
  HTTP response is derived from all its triggers' outcomes together,
  according to "x-temporal.returnStrategy" ("acceptPartial", the default, or
  "allOrNothing").

  Background:
    Given an operation whose x-temporal.triggers has more than one entry
    And each trigger has its own namespace and workflowId

  Scenario: Single-trigger operation reports that trigger's outcome directly
    Given an operation with exactly one trigger
    When the trigger succeeds
    Then the HTTP response is that trigger's own result, not a BatchResult
    And returnStrategy has no effect on a single-trigger operation

  Scenario: All triggers succeed
    Given 2 triggers on one operation
    When both triggers are dispatched and both succeed
    Then the top-level status is "WORKFLOW_STARTED"
    And the HTTP status matches the first trigger's own success status
    And this is true regardless of returnStrategy

  Scenario: Triggers are dispatched concurrently, not sequentially
    Given 2 triggers on one operation, one of which is slow to respond
    When the operation is dispatched
    Then both triggers are started before either one's result is known
    And a slow trigger does not delay the other trigger from starting

  Scenario: A failing trigger does not prevent sibling triggers from running
    Given 2 triggers, where the first trigger's Temporal call will fail
    When the operation is dispatched
    Then the second trigger is still dispatched and can still succeed
    And the failure of the first is reported in its own result item only

  Scenario Outline: acceptPartial (default) status by outcome mix
    Given returnStrategy is "acceptPartial" (or unset)
    And <succeeded> of <total> triggers succeed
    Then the top-level status is "<status>"
    And the HTTP status is <http_status>

    Examples:
      | succeeded | total | status                      | http_status |
      | 2         | 2     | WORKFLOW_STARTED             | 202         |
      | 0         | 2     | WORKFLOW_NOT_STARTED         | (first item's own failure status) |
      | 1         | 2     | WORKFLOW_PARTIALLY_STARTED   | 207         |

  Scenario Outline: allOrNothing status by outcome mix
    Given returnStrategy is "allOrNothing"
    And <succeeded> of <total> triggers succeed
    Then the top-level status is "<status>"
    And the HTTP status is <http_status>

    Examples:
      | succeeded | total | status                | http_status |
      | 2         | 2     | WORKFLOW_STARTED       | 202          |
      | 0         | 2     | WORKFLOW_NOT_STARTED   | 409          |
      | 1         | 2     | WORKFLOW_NOT_STARTED   | 409          |

  Scenario: Every trigger's own result is always present in the response
    Given a multi-trigger operation under either returnStrategy
    When the operation is dispatched
    Then the response's "results" array has one item per trigger
    And each item identifies which workflow/action it is about
    # So the caller can tell exactly which trigger(s) succeeded or failed
    # Regardless of which single top-level status/HTTP code was chosen

  Scenario: Unknown returnStrategy value fails spec validation
    Given x-temporal.returnStrategy is set to a value other than "acceptPartial" or "allOrNothing" (and not empty)
    When the spec is loaded
    Then spec loading fails before the server starts
