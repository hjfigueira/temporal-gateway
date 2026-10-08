# Code: internal/temporal/dispatch.go, internal/spec/validation.go

Feature: signalWorkflow, queryWorkflow, cancelWorkflow, terminateWorkflow, getResult
  Besides startWorkflow, a trigger's "action" may be one of signalWorkflow,
  queryWorkflow, cancelWorkflow, terminateWorkflow, or getResult. Every
  trigger, regardless of action, requires namespace and workflowId.

  Scenario: signalWorkflow requires signalName
    Given a trigger with action: signalWorkflow and no signalName
    When the spec is validated
    Then validation fails naming the missing signalName

  Scenario: signalWorkflow sends the request body as the signal's input
    Given a signalWorkflow trigger with signalName "cancelOrder"
    And the request body is {"reason": "customer request"}
    When the trigger dispatches
    Then SignalWorkflow is called with that body as input
    And the response status is "SIGNALED"

  Scenario: queryWorkflow requires queryType
    Given a trigger with action: queryWorkflow and no queryType
    When the spec is validated
    Then validation fails naming the missing queryType

  Scenario: queryWorkflow's result is returned as-is, not wrapped in an envelope
    Given a queryWorkflow trigger with queryType "getOrderState"
    When the query succeeds and returns {"state": "PENDING"}
    Then the HTTP response body is exactly {"state": "PENDING"}
    And it is not wrapped in a response.Envelope
    # The query result is caller-defined business data, not a gateway
    # operation-status acknowledgement.

  Scenario: cancelWorkflow and terminateWorkflow need no action-specific fields
    Given a trigger with action: cancelWorkflow (or terminateWorkflow)
    And only namespace and workflowId are set
    When the spec is validated
    Then validation passes

  Scenario: terminateWorkflow reads "reason" from the request body
    Given a terminateWorkflow trigger
    And the request body is {"reason": "duplicate order"}
    When the trigger dispatches
    Then TerminateWorkflow is called with reason "duplicate order"

  Scenario: terminateWorkflow accepts a plain string body as the reason too
    Given a terminateWorkflow trigger
    And the request body is the JSON string "duplicate order" (not an object)
    When the trigger dispatches
    Then TerminateWorkflow is called with reason "duplicate order"

  Scenario: getResult blocks until the workflow completes
    Given a getResult trigger for a running workflow
    When the trigger dispatches
    Then the call blocks until that workflow's current run completes
    And the decoded result is returned as-is, not wrapped in an envelope

  Scenario: An unknown action value fails spec validation
    Given a trigger's action is "frobnicateWorkflow"
    When the spec is validated
    Then validation fails naming the unknown action

  Scenario: Every action requires namespace and workflowId
    Given any trigger (of any action) with a missing namespace or workflowId
    When the spec is validated
    Then validation fails naming whichever of the two is missing
