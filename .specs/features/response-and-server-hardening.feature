# See .specs/adr/0010-response-envelope-separates-gateway-status-from-http-status.md
# See .specs/adr/0014-http-server-hardening.md
# Code: internal/response/response.go, main.go (newServer, serve)

Feature: Response envelope and HTTP server hardening
  Every response the gateway writes shares a common Envelope{Status,
  Message} contract, letting a caller tell success from a success that
  didn't do what was asked. The HTTP server itself is hardened against slow
  clients and shuts down gracefully, draining in-flight requests.

  Scenario: Every response type embeds Envelope
    Given any response the gateway writes (WorkflowStarted, WorkflowSignaled,
      WorkflowAck, BatchResult, validate.Result)
    When its JSON is inspected
    Then it includes a top-level "status" field
    And an optional "message" field

  Scenario: Status.IsError() treats a non-STARTED success as an error-shaped outcome
    Given a startWorkflow call returned no Go error
    But attached to an existing run instead of creating one (status WORKFLOW_RUNNING)
    When dispatchOutcome.succeeded() is evaluated
    Then it reports false
    And this binding is counted as not-succeeded for batch/span-attribute purposes

  Scenario: A query/getResult result is returned without an Envelope wrapper
    Given the action is queryWorkflow or getResult
    When the result is written to the HTTP response
    Then it is the decoded workflow result exactly as produced
    And it does not include a "status"/"message" envelope

  Scenario: Slow clients cannot hold a connection open indefinitely
    Given a client that sends request headers very slowly
    When it connects to the gateway
    Then the connection is closed after ReadHeaderTimeout (10s) if headers
      are not fully received by then

  Scenario: Idle keep-alive connections are closed after IdleTimeout
    Given a keep-alive connection that goes quiet
    When 60 seconds pass with no activity
    Then the server closes the idle connection

  Scenario: SIGTERM triggers a graceful shutdown that drains in-flight requests
    Given a request is currently being handled (e.g. a slow getResult call)
    When the process receives SIGTERM
    Then server.Shutdown is called with a 10s timeout
    And the in-flight request is given a chance to finish within that window
    And new connections are not accepted during shutdown

  Scenario: A request still running when the shutdown timeout elapses is dropped
    Given an in-flight request takes longer than shutdownTimeout to finish
    When the shutdown timeout (10s) elapses
    Then the connection is force-closed
    And the shutdown proceeds (does not wait indefinitely)

  Scenario: Every fallible startup stage returns an error instead of exiting directly
    Given any of: dotenv.Load, config.Load, telemetry.Setup, spec.Load,
      temporal.NewConnections, temporal.ValidateNamespaces fails
    When run() executes
    Then it returns an error immediately, propagated up to main()
    And deferred cleanup (closing Temporal connections, flushing telemetry)
      still runs before the process exits
    And os.Exit is called exactly once, in main, after run() has fully unwound
