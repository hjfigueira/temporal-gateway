# ADR-021: Read "started vs attached" from the server, not a prior describe

**Status:** Accepted — supersedes the *mechanism* of [ADR-005](../deprecated/0005-accurate-start-semantics.md) (its goal stands)

**Related features:** [`start-workflow-semantics.feature`](../../features/start-workflow-semantics.feature)

## Context

ADR-005 called `DescribeWorkflowExecution` before `ExecuteWorkflow`, then
compared RunIDs. That is a check-then-act race. Two concurrent requests for
the same ID both see "no prior run": one creates the run, the other attaches
to it under `UseExisting`, and **both** reported `STARTED`. That is exactly
the false positive ADR-005 exists to prevent. It also cost an extra RPC on
every start.

The server already answers the question atomically:
`StartWorkflowExecutionResponse.Started`. Go SDK v1.46 reads that response
but doesn't expose `Started` on `client.WorkflowRun`.

## Decision

Each Temporal client is dialed with a gRPC unary interceptor
(`internal/temporal/started.go`). It copies a successful
`StartWorkflowExecution` response's `Started` flag into a `*bool` that
`startWorkflow` puts on the call's context. When `Started` is false, the
start attached to an existing run. Only then is that run described (by its
RunID) to report its real state via `classifyExistingRun`. This describe is
informational: if it fails, the response falls back to `WORKFLOW_RUNNING`.

When the SDK swallows `WorkflowExecutionAlreadyStarted`
(`WorkflowExecutionErrorWhenAlreadyStarted: false`), the gRPC call failed,
the flag is never set, and the start correctly reads as "not started".

## Consequences

- Concurrent identical starts: exactly one reports `STARTED`. This was
  verified against a dev server with 10 concurrent requests.
- A fresh start makes one RPC, not two. Attaching costs the extra describe.
- This relies on the SDK passing the caller's context through to the gRPC
  call, which it does (it only derives timeouts from it). If an SDK upgrade
  exposes `Started` directly, the interceptor should be replaced with that.
