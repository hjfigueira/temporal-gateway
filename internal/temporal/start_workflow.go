package temporal

import (
	"context"
	"fmt"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"
	sdktemporal "go.temporal.io/sdk/temporal"

	"temporal-gateway/internal/response"
	"temporal-gateway/internal/spec"
)

// startWorkflow builds a client.StartWorkflowOptions from binding and calls
// ExecuteWorkflow. Because ExecuteWorkflow can succeed without creating a
// new run (see the comment above the call), the returned Status only
// claims StatusStarted when the server reports a fresh run was created;
// otherwise it reports the existing run's real state via
// classifyExistingRun.
func (d *Dispatcher) startWorkflow(ctx context.Context, conn *Connection, binding spec.TemporalBinding, workflowID string, body any) (any, error) {
	taskQueue := binding.TaskQueue
	if taskQueue == "" {
		taskQueue, _ = conn.Catalog.TaskQueueFor(binding.WorkflowType)
	}

	options := client.StartWorkflowOptions{
		ID:                                       workflowID,
		TaskQueue:                                taskQueue,
		WorkflowExecutionErrorWhenAlreadyStarted: binding.WorkflowExecutionErrorWhenAlreadyStarted,
		CronSchedule:                             binding.CronSchedule,
		Memo:                                     binding.Memo,
		EnableEagerStart:                         binding.EnableEagerStart,
		StaticSummary:                            binding.StaticSummary,
		StaticDetails:                            binding.StaticDetails,
	}
	options.WorkflowIDReusePolicy, options.WorkflowIDConflictPolicy =
		resolveReuseAndConflictPolicy(binding.IDReusePolicy, binding.WorkflowIDConflictPolicy)
	if len(binding.SearchAttributes) > 0 {
		attrs, err := buildTypedSearchAttributes(binding.SearchAttributes)
		if err != nil {
			return nil, fmt.Errorf("x-temporal searchAttributes: %w", err)
		}
		options.TypedSearchAttributes = attrs
	}
	if dur, ok := parseDuration(binding.WorkflowExecutionTimeout); ok {
		options.WorkflowExecutionTimeout = dur
	}
	if dur, ok := parseDuration(binding.WorkflowRunTimeout); ok {
		options.WorkflowRunTimeout = dur
	}
	if dur, ok := parseDuration(binding.WorkflowTaskTimeout); ok {
		options.WorkflowTaskTimeout = dur
	}
	if dur, ok := parseDuration(binding.StartDelay); ok {
		options.StartDelay = dur
	}
	if binding.RetryPolicy != nil {
		initial, _ := parseDuration(binding.RetryPolicy.InitialInterval)
		maximum, _ := parseDuration(binding.RetryPolicy.MaximumInterval)
		options.RetryPolicy = &sdktemporal.RetryPolicy{
			InitialInterval:        initial,
			BackoffCoefficient:     binding.RetryPolicy.BackoffCoefficient,
			MaximumInterval:        maximum,
			MaximumAttempts:        binding.RetryPolicy.MaximumAttempts,
			NonRetryableErrorTypes: binding.RetryPolicy.NonRetryableErrorTypes,
		}
	}
	if binding.Priority != nil {
		options.Priority = sdktemporal.Priority{
			PriorityKey:    binding.Priority.PriorityKey,
			FairnessKey:    binding.Priority.FairnessKey,
			FairnessWeight: binding.Priority.FairnessWeight,
		}
	}

	var args []interface{}
	if body != nil {
		args = append(args, body)
	}

	// ExecuteWorkflow can succeed (no error) without creating a new run: if
	// a workflow with this ID already exists, WorkflowIDConflictPolicy or
	// WorkflowIDReusePolicy may let the call attach to that execution and
	// return its RunID instead of erroring. The server says which happened
	// in StartWorkflowExecutionResponse.Started, captured via
	// withStartedFlag (see started.go) - atomically, unlike describing the
	// workflow first, which two concurrent starts would both see as absent.
	startCtx, started := withStartedFlag(ctx)
	run, err := conn.Client.ExecuteWorkflow(startCtx, options, binding.WorkflowType, args...)
	if err != nil {
		return nil, err
	}

	status, message := response.StatusStarted, ""
	if !*started {
		status, message = existingRunStatus(ctx, conn, run.GetID(), run.GetRunID())
	}

	return response.WorkflowStarted{
		Envelope:   response.Envelope{Status: status, Message: message},
		WorkflowID: run.GetID(),
		RunID:      run.GetRunID(),
	}, nil
}

// existingRunStatus describes the run a start attached to, to report its
// real state. It's informational only - whether a new run was created is
// already decided - so a failed describe falls back to "already running".
func existingRunStatus(ctx context.Context, conn *Connection, workflowID, runID string) (response.Status, string) {
	desc, err := conn.Client.DescribeWorkflowExecution(ctx, workflowID, runID)
	if err != nil {
		return classifyExistingRun(enumspb.WORKFLOW_EXECUTION_STATUS_UNSPECIFIED)
	}
	return classifyExistingRun(desc.GetWorkflowExecutionInfo().GetStatus())
}

// classifyExistingRun maps a pre-existing workflow's current execution
// status to the response.Status/message describing it, used when
// ExecuteWorkflow attaches to an existing run instead of starting a new
// one.
func classifyExistingRun(status enumspb.WorkflowExecutionStatus) (response.Status, string) {
	switch status {
	case enumspb.WORKFLOW_EXECUTION_STATUS_RUNNING, enumspb.WORKFLOW_EXECUTION_STATUS_CONTINUED_AS_NEW:
		return response.StatusWorkflowRunning, "a workflow with this ID is already running"
	case enumspb.WORKFLOW_EXECUTION_STATUS_COMPLETED:
		return response.StatusWorkflowCompleted, "a workflow with this ID already completed"
	case enumspb.WORKFLOW_EXECUTION_STATUS_FAILED:
		return response.StatusWorkflowFailed, "a workflow with this ID already failed"
	case enumspb.WORKFLOW_EXECUTION_STATUS_CANCELED:
		return response.StatusWorkflowCancelled, "a workflow with this ID was already cancelled"
	case enumspb.WORKFLOW_EXECUTION_STATUS_TERMINATED:
		return response.StatusWorkflowTerminated, "a workflow with this ID was already terminated"
	case enumspb.WORKFLOW_EXECUTION_STATUS_TIMED_OUT:
		return response.StatusWorkflowTimedOut, "a workflow with this ID already timed out"
	default:
		return response.StatusWorkflowRunning, "a workflow with this ID already exists"
	}
}
