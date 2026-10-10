# ADR-005: Accurate start semantics — distinguish "started" from "attached"

**Superseded by [ADR-021](../active/0021-started-flag-from-server-not-describe-first.md)** — the goal (never report an attach as STARTED) stands and is restated there; the describe-first RunID comparison was racy and is replaced by the server's `Started` flag.

**Status:** Deprecated

**Related features:** [`start-workflow-semantics.feature`](../../features/start-workflow-semantics.feature)

## Context

Temporal's `ExecuteWorkflow` can return success without creating a new run:
depending on `WorkflowIDReusePolicy` / `WorkflowIDConflictPolicy`, a call
against an in-use workflow ID can silently attach to the existing execution
and return its `RunID` instead of erroring. Reporting that as "started"
would be a false positive the caller can't detect.

## Decision

Before calling `ExecuteWorkflow`, the dispatcher
(`internal/temporal/start_workflow.go`) calls `DescribeWorkflowExecution` to
capture any pre-existing execution's `RunID` and status. After
`ExecuteWorkflow` returns, it compares the returned `RunID` to that
pre-existing one: if they match, no new run was created, and the response
reports the *real* state of the existing run (`WORKFLOW_RUNNING`,
`WORKFLOW_COMPLETED`, `WORKFLOW_FAILED`, ...) via `classifyExistingRun`,
rather than claiming `STARTED`. Comparing `RunID`s (not timestamps) is
deliberate — ordinary request latency can exceed the gap between two calls,
so a timestamp comparison would be unreliable.

## Consequences

- `startWorkflow` costs one extra `DescribeWorkflowExecution` round trip
  per call. Accepted as the price of not lying about what happened.
- `response.Status.IsError()` must treat "attached to an existing run" as
  an error-shaped outcome even though the Go `error` is `nil` — see
  [ADR-010](../active/0010-response-envelope-separates-gateway-status-from-http-status.md).
- `DescribeWorkflowExecution` returning `NotFound` is the common case (no
  prior execution) and is treated as "nothing to attach to", not a
  failure.
