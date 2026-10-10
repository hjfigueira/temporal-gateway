# See .specs/adr/deprecated/0005-accurate-start-semantics.md
# See .specs/adr/active/0021-started-flag-from-server-not-describe-first.md
# See .specs/adr/active/0006-terminateifrunning-reuse-policy-mapping.md
# See .specs/adr/active/0007-typed-search-attributes.md
# Code: internal/temporal/start_workflow.go, internal/temporal/started.go,
#       internal/temporal/policy.go,
#       internal/temporal/search_attributes.go, internal/spec/validation.go

Feature: startWorkflow semantics and options
  A startWorkflow trigger may set any of the Temporal SDK's
  StartWorkflowOptions. Critically, the gateway distinguishes a call that
  actually created a new run from one that silently attached to an
  already-existing execution, and reports each truthfully rather than
  claiming STARTED whenever the Go call itself didn't error.

  Background:
    Given a startWorkflow trigger with workflowType and taskQueue set

  Scenario: A fresh workflow ID reports STARTED
    Given no workflow execution currently exists for workflowId
    When startWorkflow dispatches
    Then a new run is created
    And the response status is "STARTED" with the new run's WorkflowID/RunID

  Scenario: Attaching to an already-running execution reports its real state
    Given a workflow with this workflowId is already RUNNING
    And the configured idReusePolicy/workflowIdConflictPolicy allows attaching instead of erroring
    When startWorkflow dispatches
    Then ExecuteWorkflow returns successfully with the existing RunID
    And the response status is "WORKFLOW_RUNNING", not "STARTED"
    And the message says a workflow with this ID is already running

  Scenario Outline: Attaching to a completed-state execution reports that state
    Given a workflow with this workflowId previously ended in state "<prior_state>"
    And the configured policy allows attaching instead of erroring
    When startWorkflow dispatches and the server reports it did not start a run
    Then the attached run is described by its RunID
    And the response status is "<status>"

    Examples:
      | prior_state | status               |
      | COMPLETED   | WORKFLOW_COMPLETED   |
      | FAILED      | WORKFLOW_FAILED      |
      | CANCELED    | WORKFLOW_CANCELLED   |
      | TERMINATED  | WORKFLOW_TERMINATED  |
      | TIMED_OUT   | WORKFLOW_TIMED_OUT   |

  Scenario: The server's Started flag, not a prior describe, decides started vs attached
    When ExecuteWorkflow's StartWorkflowExecution response has Started = true
    Then the trigger is classified as "started a new run"
    But when Started is false, or the start call failed with AlreadyStarted and the SDK returned the existing run instead of an error
    Then the trigger is classified as "attached to existing run"
    # No DescribeWorkflowExecution happens before the start, so a fresh
    # start costs one RPC.

  Scenario: Concurrent identical starts report STARTED exactly once
    Given workflowIdConflictPolicy is "UseExisting"
    And no execution exists yet for workflowId
    When 10 identical startWorkflow requests arrive concurrently
    Then exactly one response has status "STARTED"
    And the other nine report "WORKFLOW_RUNNING" with the same RunID

  Scenario: A failed describe of the attached run falls back to WORKFLOW_RUNNING
    Given startWorkflow attached to an existing run
    And describing that run fails
    Then the response status is "WORKFLOW_RUNNING"
    And it is never reported as "STARTED"

  Scenario: idReusePolicy "TerminateIfRunning" maps to AllowDuplicate + TerminateExisting
    Given a trigger sets idReusePolicy: TerminateIfRunning
    And does not set workflowIdConflictPolicy
    When the StartWorkflowOptions are built
    Then WorkflowIDReusePolicy is AllowDuplicate
    And WorkflowIDConflictPolicy is TerminateExisting

  Scenario: TerminateIfRunning cannot be combined with an explicit conflict policy
    Given a trigger sets idReusePolicy: TerminateIfRunning
    And also sets workflowIdConflictPolicy: Fail
    When the spec is validated
    Then validation fails at load time
    And the gateway does not start

  Scenario: cronSchedule and startDelay are mutually exclusive
    Given a trigger sets both cronSchedule and startDelay
    When the spec is validated
    Then validation fails naming both fields

  Scenario: Typed search attributes require an explicit type per entry
    Given searchAttributes: [{name: CustomStringField, type: string, value: widget}]
    When the trigger dispatches
    Then the SDK's typed SearchAttributeKeyString is used to set it
    And a plain untyped map is never sent to Temporal

  Scenario Outline: Search attribute value must match its declared type
    Given a search attribute of type "<type>" with value <value>
    When the spec is validated
    Then validation <result>

    Examples:
      | type    | value          | result  |
      | string  | "widget"       | passes  |
      | string  | 123            | fails   |
      | bool    | true           | passes  |
      | int     | 42              | passes  |
      | time    | "2024-01-01T00:00:00Z" | passes |
      | time    | "not-a-date"   | fails   |
      | keywordList | ["a", "b"] | passes  |
      | keywordList | "a"        | fails   |

  Scenario: A duration field must parse as a Go duration string
    Given workflowExecutionTimeout is set to "24h"
    When the spec is validated
    Then it parses successfully via time.ParseDuration
    But a value like "24 hours" or "1 day" fails spec validation

  Scenario: Request body becomes the workflow's input argument
    Given the request has a JSON body
    When startWorkflow dispatches
    Then the decoded body is passed as ExecuteWorkflow's input argument
    But when there is no body, no argument is passed at all

  Scenario: An explicit taskQueue always wins over the catalog default
    Given temporal.workflows declares a default taskQueue for this workflowType
    And the trigger also sets its own taskQueue explicitly
    When startWorkflow dispatches
    Then the trigger's own taskQueue is used, not the catalog's
