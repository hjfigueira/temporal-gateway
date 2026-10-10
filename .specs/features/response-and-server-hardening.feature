# See .specs/adr/active/0010-response-envelope-separates-gateway-status-from-http-status.md
# See .specs/adr/active/0014-http-server-hardening.md
# See .specs/adr/active/0022-per-request-limits-and-sanitized-errors.md
# Code: internal/response/response.go, main.go (newServer, serve),
#       internal/gateway/dispatch_handler.go (body limit, deadline, errorResponse)

Feature: Response envelope and HTTP server hardening
  Every response the gateway writes shares a common Envelope{Status,
  Message} contract, letting a caller tell success from a success that
  didn't do what was asked. The HTTP server itself is hardened against slow
  clients and shuts down gracefully, draining in-flight requests.

  Scenario: Every response type embeds Envelope
    Given any response the gateway writes (WorkflowStarted, WorkflowSignaled, WorkflowAck, BatchResult, ValidationFailed)
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
    Then the connection is closed after ReadHeaderTimeout (10s) if headers are not fully received by then

  Scenario: A body larger than server.maxBodyBytes is rejected with 413
    Given server.maxBodyBytes is unset (default 1 MiB)
    When a request body larger than 1 MiB is sent
    Then the response is 413 with status "PAYLOAD_TOO_LARGE"
    And nothing is dispatched to Temporal

  Scenario: A dispatch exceeding server.requestTimeout is answered with 504
    Given server.requestTimeout is "2s"
    And a getResult route targets a workflow that doesn't complete in time
    When the request is made
    Then after about 2s the response is 504 with status "TIMEOUT"
    And the Temporal call is cancelled rather than left running

  Scenario: The write timeout always trails the request timeout
    Given server.requestTimeout is "2m"
    When the server is built
    Then its WriteTimeout is 2m5s
    # So the handler's own 504 is written before the server drops the
    # connection.

  Scenario: Malformed request limits are rejected at config load
    Given server.maxBodyBytes is negative, or server.requestTimeout is not a positive duration
    When the config is loaded
    Then loading fails naming the offending field

  Scenario: An unavailable Temporal is answered with 503
    Given a route's namespace is not connected yet, or Temporal reports Unavailable
    When the request is dispatched
    Then the response is 503 with status "UNAVAILABLE"

  Scenario: Raw Temporal errors are not returned to callers
    Given a dispatch fails with an error naming hosts or namespaces
    When the response is written
    Then the message is a fixed per-class text (e.g. "temporal request failed") prefixed with the workflow label
    And the raw error appears only in the gateway's logs

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
    Given any of: loading .env, config.Load, telemetry.Setup, spec.Load, temporal.ValidateBindings, or temporal Connections.Connect (giving up) fails
    When run() executes
    Then it returns an error immediately, propagated up to main()
    And deferred cleanup (closing Temporal connections, flushing telemetry) still runs before the process exits
    And os.Exit is called exactly once, in main, after run() has fully unwound
