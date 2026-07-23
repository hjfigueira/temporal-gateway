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
// new run (see the priorRunID comment below), the returned Status only
// claims StatusStarted when a fresh run was actually created; otherwise it
// reports the pre-existing run's real state via classifyExistingRun.
func (d *Dispatcher) startWorkflow(ctx context.Context, binding spec.TemporalBinding, workflowID string, body any) (any, error) {
	taskQueue := binding.TaskQueue
	if taskQueue == "" {
		taskQueue, _ = d.catalog.TaskQueueFor(binding.WorkflowType)
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
	// WorkflowIDReusePolicy may allow the call to silently attach to that
	// existing execution and return its RunID instead of erroring - see
	// client.StartWorkflowOptions.WorkflowExecutionErrorWhenAlreadyStarted.
	// Capture any such execution's identity and status before starting, so
	// we can tell the two cases apart by comparing RunIDs afterwards -
	// comparing timestamps instead would be unreliable, since ordinary
	// request latency can easily exceed the gap between two calls.
	var priorRunID string
	var priorStatus enumspb.WorkflowExecutionStatus
	if desc, descErr := d.client.DescribeWorkflowExecution(ctx, workflowID, ""); descErr == nil {
		if info := desc.GetWorkflowExecutionInfo(); info != nil {
			priorRunID = info.GetExecution().GetRunId()
			priorStatus = info.GetStatus()
		}
	}
	// If the describe call errors (most commonly NotFound, meaning no
	// workflow with this ID exists yet), priorRunID stays empty, which
	// below is correctly read as "nothing to attach to".

	run, err := d.client.ExecuteWorkflow(ctx, options, binding.WorkflowType, args...)
	if err != nil {
		return nil, err
	}

	status, message := response.StatusStarted, ""
	if priorRunID != "" && run.GetRunID() == priorRunID {
		status, message = classifyExistingRun(priorStatus)
	}

	return response.WorkflowStarted{
		Envelope:   response.Envelope{Status: status, Message: message},
		WorkflowID: run.GetID(),
		RunID:      run.GetRunID(),
	}, nil
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
