package spec

import (
	"errors"
	"fmt"
	"time"
)

// validIDReusePolicies mirrors the WorkflowIDReusePolicy values the
// dispatcher understands (see internal/temporal.parseIDReusePolicy). Kept
// here, rather than importing the Temporal SDK, so the spec package stays
// free of transport-specific dependencies.
var validIDReusePolicies = map[string]bool{
	"":                         true,
	"AllowDuplicate":           true,
	"AllowDuplicateFailedOnly": true,
	"RejectDuplicate":          true,
	"TerminateIfRunning":       true,
}

// validIDConflictPolicies mirrors the WorkflowIDConflictPolicy values the
// dispatcher understands (see internal/temporal.parseIDConflictPolicy).
var validIDConflictPolicies = map[string]bool{
	"":                  true,
	"Fail":              true,
	"UseExisting":       true,
	"TerminateExisting": true,
}

// validSearchAttributeTypes mirrors the SearchAttribute.Type values the
// dispatcher understands (see internal/temporal.searchAttributeUpdate),
// each corresponding to one of the SDK's typed search attribute key
// constructors.
var validSearchAttributeTypes = map[string]bool{
	"string":      true,
	"keyword":     true,
	"bool":        true,
	"int":         true,
	"float":       true,
	"time":        true,
	"keywordList": true,
}

// validReturnStrategies mirrors the TemporalSpec.ReturnStrategy values
// internal/gateway's writeBatchResult understands. "" is valid - it means
// Strategy() falls back to ReturnStrategyAcceptPartial.
var validReturnStrategies = map[ReturnStrategy]bool{
	"":                          true,
	ReturnStrategyAcceptPartial: true,
	ReturnStrategyAllOrNothing:  true,
}

// validate checks every operation's x-temporal bindings and returns a
// single error joining every problem found (via errors.Error(), one per
// line), rather than stopping at the first, so a misconfigured spec can be
// fixed in one pass instead of being rediscovered error-by-error.
func (s *Spec) validate() error {
	var errs []error
	for path, item := range s.Paths {
		for method, op := range item.operations() {
			if len(op.Temporal.Triggers) == 0 {
				errs = append(errs, fmt.Errorf("%s %s: missing x-temporal.triggers", method, path))
				continue
			}
			if !validReturnStrategies[op.Temporal.ReturnStrategy] {
				errs = append(errs, fmt.Errorf("%s %s: x-temporal: unknown returnStrategy %q", method, path, op.Temporal.ReturnStrategy))
			}
			for i, t := range op.Temporal.Triggers {
				errs = append(errs, validateBinding(method, path, i, t)...)
			}
		}
	}
	return errors.Join(errs...)
}

// validateBinding checks one x-temporal.triggers[i] entry against the rules
// for its declared Action, returning every problem found (not just the
// first).
func validateBinding(method, path string, i int, t TemporalBinding) []error {
	var errs []error

	if t.Action == "" {
		errs = append(errs, fmt.Errorf("%s %s: x-temporal.triggers[%d]: missing action", method, path, i))
	}
	if t.Namespace == "" {
		errs = append(errs, fmt.Errorf("%s %s: x-temporal.triggers[%d]: missing namespace", method, path, i))
	}

	switch t.Action {
	case ActionStartWorkflow:
		errs = append(errs, validateStartWorkflowBinding(method, path, i, t)...)
	case ActionSignalWorkflow:
		if t.SignalName == "" {
			errs = append(errs, fmt.Errorf("%s %s: x-temporal.triggers[%d]: signalWorkflow requires signalName", method, path, i))
		}
	case ActionQueryWorkflow:
		if t.QueryType == "" {
			errs = append(errs, fmt.Errorf("%s %s: x-temporal.triggers[%d]: queryWorkflow requires queryType", method, path, i))
		}
	case ActionCancelWorkflow, ActionTerminateWorkflow, ActionGetResult:
		// no action-specific required fields beyond workflowId.
	case "":
		// already reported above as "missing action".
	default:
		errs = append(errs, fmt.Errorf("%s %s: x-temporal.triggers[%d]: unknown action %q", method, path, i, t.Action))
	}

	if t.WorkflowID == "" {
		errs = append(errs, fmt.Errorf("%s %s: x-temporal.triggers[%d]: missing workflowId", method, path, i))
	}

	return errs
}

// validateStartWorkflowBinding checks the fields that only apply to
// ActionStartWorkflow: the required workflowType/taskQueue, the ID
// reuse/conflict policies, the cronSchedule/startDelay/retryPolicy/timeout
// durations, and any declared searchAttributes.
func validateStartWorkflowBinding(method, path string, i int, t TemporalBinding) []error {
	var errs []error

	if t.WorkflowType == "" {
		errs = append(errs, fmt.Errorf("%s %s: x-temporal.triggers[%d]: startWorkflow requires workflowType", method, path, i))
	}
	if t.TaskQueue == "" {
		errs = append(errs, fmt.Errorf("%s %s: x-temporal.triggers[%d]: startWorkflow requires taskQueue", method, path, i))
	}
	if !validIDReusePolicies[t.IDReusePolicy] {
		errs = append(errs, fmt.Errorf("%s %s: x-temporal.triggers[%d]: unknown idReusePolicy %q", method, path, i, t.IDReusePolicy))
	}
	if !validIDConflictPolicies[t.WorkflowIDConflictPolicy] {
		errs = append(errs, fmt.Errorf("%s %s: x-temporal.triggers[%d]: unknown workflowIdConflictPolicy %q", method, path, i, t.WorkflowIDConflictPolicy))
	}
	if t.IDReusePolicy == "TerminateIfRunning" && t.WorkflowIDConflictPolicy != "" {
		errs = append(errs, fmt.Errorf("%s %s: x-temporal.triggers[%d]: idReusePolicy TerminateIfRunning cannot be combined with workflowIdConflictPolicy (TerminateIfRunning already implies TerminateExisting)", method, path, i))
	}
	if t.CronSchedule != "" && t.StartDelay != "" {
		errs = append(errs, fmt.Errorf("%s %s: x-temporal.triggers[%d]: cronSchedule and startDelay cannot both be set", method, path, i))
	}

	durations := []struct{ name, value string }{
		{"workflowExecutionTimeout", t.WorkflowExecutionTimeout},
		{"workflowRunTimeout", t.WorkflowRunTimeout},
		{"workflowTaskTimeout", t.WorkflowTaskTimeout},
		{"startDelay", t.StartDelay},
	}
	if t.RetryPolicy != nil {
		durations = append(durations,
			struct{ name, value string }{"retryPolicy.initialInterval", t.RetryPolicy.InitialInterval},
			struct{ name, value string }{"retryPolicy.maximumInterval", t.RetryPolicy.MaximumInterval},
		)
	}
	for _, d := range durations {
		if d.value == "" {
			continue
		}
		if _, err := time.ParseDuration(d.value); err != nil {
			errs = append(errs, fmt.Errorf("%s %s: x-temporal.triggers[%d]: invalid %s %q: %w", method, path, i, d.name, d.value, err))
		}
	}

	for j, sa := range t.SearchAttributes {
		if sa.Name == "" {
			errs = append(errs, fmt.Errorf("%s %s: x-temporal.triggers[%d]: searchAttributes[%d]: missing name", method, path, i, j))
		}
		if !validSearchAttributeTypes[sa.Type] {
			errs = append(errs, fmt.Errorf("%s %s: x-temporal.triggers[%d]: searchAttributes[%d]: unknown type %q", method, path, i, j, sa.Type))
			continue
		}
		if err := validateSearchAttributeValue(sa); err != nil {
			errs = append(errs, fmt.Errorf("%s %s: x-temporal.triggers[%d]: searchAttributes[%d] %q: %w", method, path, i, j, sa.Name, err))
		}
	}

	return errs
}

// validateSearchAttributeValue checks that sa.Value's shape matches its
// declared Type, so a mismatch is caught at spec-load time rather than
// surfacing as a dispatch-time error on the first request. Assumes
// validSearchAttributeTypes[sa.Type] is already true.
func validateSearchAttributeValue(sa SearchAttribute) error {
	switch sa.Type {
	case "string", "keyword":
		if _, ok := sa.Value.(string); !ok {
			return fmt.Errorf("value must be a string for type %q", sa.Type)
		}
	case "bool":
		if _, ok := sa.Value.(bool); !ok {
			return fmt.Errorf("value must be a boolean for type %q", sa.Type)
		}
	case "int":
		switch sa.Value.(type) {
		case int, int64:
		default:
			return fmt.Errorf("value must be an integer for type %q", sa.Type)
		}
	case "float":
		switch sa.Value.(type) {
		case float64, float32, int, int64:
		default:
			return fmt.Errorf("value must be a number for type %q", sa.Type)
		}
	case "time":
		s, ok := sa.Value.(string)
		if !ok {
			return fmt.Errorf("value must be an RFC3339 string for type %q", sa.Type)
		}
		if _, err := time.Parse(time.RFC3339, s); err != nil {
			return fmt.Errorf("value %q is not a valid RFC3339 timestamp: %w", s, err)
		}
	case "keywordList":
		list, ok := sa.Value.([]any)
		if !ok {
			return fmt.Errorf("value must be a list of strings for type %q", sa.Type)
		}
		for _, v := range list {
			if _, ok := v.(string); !ok {
				return fmt.Errorf("value must be a list of strings for type %q", sa.Type)
			}
		}
	}
	return nil
}
