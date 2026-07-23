package temporal

import (
	"context"
	"fmt"
	"time"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"
	sdktemporal "go.temporal.io/sdk/temporal"

	"temporal-gateway/internal/response"
	"temporal-gateway/internal/spec"
)

// Dispatcher executes the Temporal action described by a route's
// x-temporal binding against a real Temporal client.
type Dispatcher struct {
	client  client.Client
	catalog *Catalog
}

func NewDispatcher(c client.Client, catalog *Catalog) *Dispatcher {
	return &Dispatcher{client: c, catalog: catalog}
}

// Dispatch runs binding's action against workflowID, using body (may be
// nil) as the workflow/signal/query input where applicable. It returns a
// JSON-serializable result.
func (d *Dispatcher) Dispatch(ctx context.Context, binding spec.TemporalBinding, workflowID string, body any) (any, error) {
	switch binding.Action {
	case spec.ActionStartWorkflow:
		return d.startWorkflow(ctx, binding, workflowID, body)
	case spec.ActionSignalWorkflow:
		if err := d.client.SignalWorkflow(ctx, workflowID, "", binding.SignalName, body); err != nil {
			return nil, err
		}
		return map[string]string{"workflowId": workflowID, "signalName": binding.SignalName}, nil
	case spec.ActionQueryWorkflow:
		return d.queryWorkflow(ctx, binding, workflowID, body)
	case spec.ActionCancelWorkflow:
		if err := d.client.CancelWorkflow(ctx, workflowID, ""); err != nil {
			return nil, err
		}
		return map[string]string{"workflowId": workflowID, "status": "cancellation requested"}, nil
	case spec.ActionTerminateWorkflow:
		reason, _ := body.(string)
		if m, ok := body.(map[string]any); ok {
			if r, ok := m["reason"].(string); ok {
				reason = r
			}
		}
		if err := d.client.TerminateWorkflow(ctx, workflowID, "", reason); err != nil {
			return nil, err
		}
		return map[string]string{"workflowId": workflowID, "status": "terminated"}, nil
	case spec.ActionGetResult:
		return d.getResult(ctx, workflowID)
	default:
		return nil, fmt.Errorf("unsupported temporal action %q", binding.Action)
	}
}

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
		SearchAttributes:                         binding.SearchAttributes,
		EnableEagerStart:                         binding.EnableEagerStart,
		StaticSummary:                            binding.StaticSummary,
		StaticDetails:                            binding.StaticDetails,
	}
	if policy, ok := parseIDReusePolicy(binding.IDReusePolicy); ok {
		options.WorkflowIDReusePolicy = policy
	}
	if policy, ok := parseIDConflictPolicy(binding.WorkflowIDConflictPolicy); ok {
		options.WorkflowIDConflictPolicy = policy
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

	run, err := d.client.ExecuteWorkflow(ctx, options, binding.WorkflowType, args...)
	if err != nil {
		return nil, err
	}

	return response.WorkflowStarted{
		Envelope:   response.Envelope{Success: true, Message: "workflow started"},
		WorkflowID: run.GetID(),
		RunID:      run.GetRunID(),
	}, nil
}

func (d *Dispatcher) queryWorkflow(ctx context.Context, binding spec.TemporalBinding, workflowID string, body any) (any, error) {
	var args []interface{}
	if body != nil {
		args = append(args, body)
	}

	value, err := d.client.QueryWorkflow(ctx, workflowID, "", binding.QueryType, args...)
	if err != nil {
		return nil, err
	}

	var result any
	if err := value.Get(&result); err != nil {
		return nil, err
	}
	return result, nil
}

func (d *Dispatcher) getResult(ctx context.Context, workflowID string) (any, error) {
	run := d.client.GetWorkflow(ctx, workflowID, "")

	var result any
	if err := run.Get(ctx, &result); err != nil {
		return nil, err
	}
	return result, nil
}

// parseIDReusePolicy maps the human-readable policy names used in
// x-temporal.idReusePolicy to the SDK enum.
func parseIDReusePolicy(name string) (enumspb.WorkflowIdReusePolicy, bool) {
	switch name {
	case "":
		return enumspb.WORKFLOW_ID_REUSE_POLICY_UNSPECIFIED, false
	case "AllowDuplicate":
		return enumspb.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE, true
	case "AllowDuplicateFailedOnly":
		return enumspb.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE_FAILED_ONLY, true
	case "RejectDuplicate":
		return enumspb.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE, true
	case "TerminateIfRunning":
		return enumspb.WORKFLOW_ID_REUSE_POLICY_TERMINATE_IF_RUNNING, true
	default:
		return enumspb.WORKFLOW_ID_REUSE_POLICY_UNSPECIFIED, false
	}
}

// parseIDConflictPolicy maps the human-readable policy names used in
// x-temporal.workflowIdConflictPolicy to the SDK enum.
func parseIDConflictPolicy(name string) (enumspb.WorkflowIdConflictPolicy, bool) {
	switch name {
	case "Fail":
		return enumspb.WORKFLOW_ID_CONFLICT_POLICY_FAIL, true
	case "UseExisting":
		return enumspb.WORKFLOW_ID_CONFLICT_POLICY_USE_EXISTING, true
	case "TerminateExisting":
		return enumspb.WORKFLOW_ID_CONFLICT_POLICY_TERMINATE_EXISTING, true
	default:
		return enumspb.WORKFLOW_ID_CONFLICT_POLICY_UNSPECIFIED, false
	}
}

// parseDuration parses an x-temporal duration string (e.g. "30s", "5m").
// spec.validate rejects malformed durations at load time, so a parse
// failure here is treated the same as the field being unset.
func parseDuration(s string) (time.Duration, bool) {
	if s == "" {
		return 0, false
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, false
	}
	return d, true
}
