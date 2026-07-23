package temporal

import (
	"time"

	enumspb "go.temporal.io/api/enums/v1"
)

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
