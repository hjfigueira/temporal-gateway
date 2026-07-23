package temporal

import (
	"testing"

	enumspb "go.temporal.io/api/enums/v1"
)

func TestResolveReuseAndConflictPolicyTranslatesTerminateIfRunning(t *testing.T) {
	// WORKFLOW_ID_REUSE_POLICY_TERMINATE_IF_RUNNING is deprecated by the
	// Temporal API in favor of this exact combination; "TerminateIfRunning"
	// must never be sent to the server as the raw deprecated enum value.
	reuse, conflict := resolveReuseAndConflictPolicy("TerminateIfRunning", "")
	if reuse != enumspb.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE {
		t.Errorf("reuse policy = %v, want ALLOW_DUPLICATE", reuse)
	}
	if conflict != enumspb.WORKFLOW_ID_CONFLICT_POLICY_TERMINATE_EXISTING {
		t.Errorf("conflict policy = %v, want TERMINATE_EXISTING", conflict)
	}
	// Deliberately referencing the deprecated value: this assertion only
	// has meaning if it names the exact thing that must never be produced.
	//goland:noinspection GoDeprecation
	if reuse == enumspb.WORKFLOW_ID_REUSE_POLICY_TERMINATE_IF_RUNNING {
		t.Error("must not use the deprecated TERMINATE_IF_RUNNING value")
	}
}

func TestResolveReuseAndConflictPolicyPassesThroughOtherwise(t *testing.T) {
	tests := []struct {
		name         string
		idReuse      string
		idConflict   string
		wantReuse    enumspb.WorkflowIdReusePolicy
		wantConflict enumspb.WorkflowIdConflictPolicy
	}{
		{
			name:         "both unset",
			wantReuse:    enumspb.WORKFLOW_ID_REUSE_POLICY_UNSPECIFIED,
			wantConflict: enumspb.WORKFLOW_ID_CONFLICT_POLICY_UNSPECIFIED,
		},
		{
			name:         "reject duplicate with use existing",
			idReuse:      "RejectDuplicate",
			idConflict:   "UseExisting",
			wantReuse:    enumspb.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
			wantConflict: enumspb.WORKFLOW_ID_CONFLICT_POLICY_USE_EXISTING,
		},
		{
			name:         "allow duplicate with terminate existing set explicitly",
			idReuse:      "AllowDuplicate",
			idConflict:   "TerminateExisting",
			wantReuse:    enumspb.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE,
			wantConflict: enumspb.WORKFLOW_ID_CONFLICT_POLICY_TERMINATE_EXISTING,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotReuse, gotConflict := resolveReuseAndConflictPolicy(tt.idReuse, tt.idConflict)
			if gotReuse != tt.wantReuse {
				t.Errorf("reuse policy = %v, want %v", gotReuse, tt.wantReuse)
			}
			if gotConflict != tt.wantConflict {
				t.Errorf("conflict policy = %v, want %v", gotConflict, tt.wantConflict)
			}
		})
	}
}
