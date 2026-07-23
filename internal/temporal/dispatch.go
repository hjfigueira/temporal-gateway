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
		return response.WorkflowSignaled{
			Envelope:   response.Envelope{Status: response.StatusSignaled},
			WorkflowID: workflowID,
			SignalName: binding.SignalName,
		}, nil
	case spec.ActionQueryWorkflow:
		return d.queryWorkflow(ctx, binding, workflowID, body)
	case spec.ActionCancelWorkflow:
		if err := d.client.CancelWorkflow(ctx, workflowID, ""); err != nil {
			return nil, err
		}
		return response.WorkflowAck{
			Envelope:   response.Envelope{Status: response.StatusCancelled},
			WorkflowID: workflowID,
		}, nil
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
		return response.WorkflowAck{
			Envelope:   response.Envelope{Status: response.StatusTerminated},
			WorkflowID: workflowID,
		}, nil
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

// buildTypedSearchAttributes converts x-temporal's YAML-friendly
// {name, type, value} search attribute list into the SDK's typed
// search attribute collection. client.StartWorkflowOptions.SearchAttributes
// (a plain map[string]interface{}) is deprecated in favor of
// TypedSearchAttributes, which requires each attribute to be built through
// an explicitly typed key (SearchAttributeKeyString, SearchAttributeKeyBool,
// ...) - hence needing to know each attribute's declared Type here.
// spec.validate already checked every attribute's Value matches its Type,
// so a conversion error here indicates a bug in that validation rather than
// bad input.
func buildTypedSearchAttributes(attrs []spec.SearchAttribute) (sdktemporal.SearchAttributes, error) {
	updates := make([]sdktemporal.SearchAttributeUpdate, 0, len(attrs))
	for _, sa := range attrs {
		update, err := searchAttributeUpdate(sa)
		if err != nil {
			return sdktemporal.SearchAttributes{}, fmt.Errorf("%q: %w", sa.Name, err)
		}
		updates = append(updates, update)
	}
	return sdktemporal.NewSearchAttributes(updates...), nil
}

// searchAttributeUpdate builds the SDK update for a single search
// attribute, dispatching on its declared Type.
func searchAttributeUpdate(sa spec.SearchAttribute) (sdktemporal.SearchAttributeUpdate, error) {
	switch sa.Type {
	case "string":
		v, ok := sa.Value.(string)
		if !ok {
			return nil, fmt.Errorf("value must be a string")
		}
		return sdktemporal.NewSearchAttributeKeyString(sa.Name).ValueSet(v), nil
	case "keyword":
		v, ok := sa.Value.(string)
		if !ok {
			return nil, fmt.Errorf("value must be a string")
		}
		return sdktemporal.NewSearchAttributeKeyKeyword(sa.Name).ValueSet(v), nil
	case "bool":
		v, ok := sa.Value.(bool)
		if !ok {
			return nil, fmt.Errorf("value must be a boolean")
		}
		return sdktemporal.NewSearchAttributeKeyBool(sa.Name).ValueSet(v), nil
	case "int":
		v, ok := toInt64(sa.Value)
		if !ok {
			return nil, fmt.Errorf("value must be an integer")
		}
		return sdktemporal.NewSearchAttributeKeyInt64(sa.Name).ValueSet(v), nil
	case "float":
		v, ok := toFloat64(sa.Value)
		if !ok {
			return nil, fmt.Errorf("value must be a number")
		}
		return sdktemporal.NewSearchAttributeKeyFloat64(sa.Name).ValueSet(v), nil
	case "time":
		s, ok := sa.Value.(string)
		if !ok {
			return nil, fmt.Errorf("value must be an RFC3339 string")
		}
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			return nil, fmt.Errorf("value %q is not a valid RFC3339 timestamp: %w", s, err)
		}
		return sdktemporal.NewSearchAttributeKeyTime(sa.Name).ValueSet(t), nil
	case "keywordList":
		list, ok := sa.Value.([]any)
		if !ok {
			return nil, fmt.Errorf("value must be a list of strings")
		}
		values := make([]string, len(list))
		for i, v := range list {
			s, ok := v.(string)
			if !ok {
				return nil, fmt.Errorf("value must be a list of strings")
			}
			values[i] = s
		}
		return sdktemporal.NewSearchAttributeKeyKeywordList(sa.Name).ValueSet(values), nil
	default:
		return nil, fmt.Errorf("unknown type %q", sa.Type)
	}
}

// toInt64 coerces a YAML-decoded numeric value (gopkg.in/yaml.v3 decodes
// plain integers as int) into an int64.
func toInt64(v any) (int64, bool) {
	switch n := v.(type) {
	case int64:
		return n, true
	case int:
		return int64(n), true
	}
	return 0, false
}

// toFloat64 coerces a YAML-decoded numeric value into a float64.
// gopkg.in/yaml.v3 decodes a whole number written without a decimal point
// as int even when the schema calls for a float, so int/int64 must be
// accepted too.
func toFloat64(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	}
	return 0, false
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

// resolveReuseAndConflictPolicy resolves an x-temporal binding's
// idReusePolicy and workflowIdConflictPolicy into the SDK enums to send to
// Temporal. "TerminateIfRunning" is translated into
// AllowDuplicate+TerminateExisting, the non-deprecated combination the
// Temporal API docs recommend in place of the deprecated
// WORKFLOW_ID_REUSE_POLICY_TERMINATE_IF_RUNNING value; spec.validate
// rejects combining "TerminateIfRunning" with an explicit
// workflowIdConflictPolicy, so it's safe to set both here unconditionally.
func resolveReuseAndConflictPolicy(idReusePolicy, workflowIDConflictPolicy string) (enumspb.WorkflowIdReusePolicy, enumspb.WorkflowIdConflictPolicy) {
	if idReusePolicy == "TerminateIfRunning" {
		return enumspb.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE, enumspb.WORKFLOW_ID_CONFLICT_POLICY_TERMINATE_EXISTING
	}

	reuse, _ := parseIDReusePolicy(idReusePolicy)
	conflict, _ := parseIDConflictPolicy(workflowIDConflictPolicy)
	return reuse, conflict
}

// parseIDReusePolicy maps the human-readable policy names used in
// x-temporal.idReusePolicy to the SDK enum. "TerminateIfRunning" is handled
// separately by resolveReuseAndConflictPolicy, so it's deliberately not
// mapped here.
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
