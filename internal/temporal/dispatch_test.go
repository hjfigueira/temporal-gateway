package temporal

import (
	"testing"
	"time"

	enumspb "go.temporal.io/api/enums/v1"
	sdktemporal "go.temporal.io/sdk/temporal"

	"temporal-gateway/internal/response"
	"temporal-gateway/internal/spec"
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

func TestBuildTypedSearchAttributes(t *testing.T) {
	attrs := []spec.SearchAttribute{
		{Name: "CustomStringField", Type: "string", Value: "widget"},
		{Name: "CustomKeywordField", Type: "keyword", Value: "gold"},
		{Name: "CustomBoolField", Type: "bool", Value: true},
		{Name: "CustomIntField", Type: "int", Value: 42},
		{Name: "CustomFloatField", Type: "float", Value: 3.5},
		{Name: "CustomTimeField", Type: "time", Value: "2026-01-02T15:04:05Z"},
		{Name: "CustomKeywordListField", Type: "keywordList", Value: []any{"a", "b"}},
	}

	sa, err := buildTypedSearchAttributes(attrs)
	if err != nil {
		t.Fatalf("buildTypedSearchAttributes returned error: %v", err)
	}
	if sa.Size() != len(attrs) {
		t.Fatalf("Size() = %d, want %d", sa.Size(), len(attrs))
	}

	if v, ok := sa.GetString(sdktemporal.NewSearchAttributeKeyString("CustomStringField")); !ok || v != "widget" {
		t.Errorf("CustomStringField = (%q, %v), want (\"widget\", true)", v, ok)
	}
	if v, ok := sa.GetKeyword(sdktemporal.NewSearchAttributeKeyKeyword("CustomKeywordField")); !ok || v != "gold" {
		t.Errorf("CustomKeywordField = (%q, %v), want (\"gold\", true)", v, ok)
	}
	if v, ok := sa.GetBool(sdktemporal.NewSearchAttributeKeyBool("CustomBoolField")); !ok || v != true {
		t.Errorf("CustomBoolField = (%v, %v), want (true, true)", v, ok)
	}
	if v, ok := sa.GetInt64(sdktemporal.NewSearchAttributeKeyInt64("CustomIntField")); !ok || v != 42 {
		t.Errorf("CustomIntField = (%d, %v), want (42, true)", v, ok)
	}
	if v, ok := sa.GetFloat64(sdktemporal.NewSearchAttributeKeyFloat64("CustomFloatField")); !ok || v != 3.5 {
		t.Errorf("CustomFloatField = (%v, %v), want (3.5, true)", v, ok)
	}
	wantTime, _ := time.Parse(time.RFC3339, "2026-01-02T15:04:05Z")
	if v, ok := sa.GetTime(sdktemporal.NewSearchAttributeKeyTime("CustomTimeField")); !ok || !v.Equal(wantTime) {
		t.Errorf("CustomTimeField = (%v, %v), want (%v, true)", v, ok, wantTime)
	}
	if v, ok := sa.GetKeywordList(sdktemporal.NewSearchAttributeKeyKeywordList("CustomKeywordListField")); !ok || len(v) != 2 || v[0] != "a" || v[1] != "b" {
		t.Errorf("CustomKeywordListField = (%v, %v), want ([a b], true)", v, ok)
	}
}

func TestSearchAttributeUpdateRejectsTypeMismatch(t *testing.T) {
	// spec.validate is expected to catch these before they ever reach here,
	// but searchAttributeUpdate must fail safe (not panic, not silently
	// coerce) if it's ever handed a mismatched value anyway.
	tests := []spec.SearchAttribute{
		{Name: "n", Type: "string", Value: 1},
		{Name: "n", Type: "bool", Value: "true"},
		{Name: "n", Type: "int", Value: "1"},
		{Name: "n", Type: "float", Value: "1.5"},
		{Name: "n", Type: "time", Value: "not-a-timestamp"},
		{Name: "n", Type: "keywordList", Value: "not-a-list"},
		{Name: "n", Type: "bogus", Value: "x"},
	}

	for _, tt := range tests {
		t.Run(tt.Type, func(t *testing.T) {
			if _, err := searchAttributeUpdate(tt); err == nil {
				t.Errorf("searchAttributeUpdate(%+v) expected an error, got nil", tt)
			}
		})
	}
}

func TestClassifyExistingRun(t *testing.T) {
	tests := []struct {
		name   string
		status enumspb.WorkflowExecutionStatus
		want   response.Status
	}{
		{"running", enumspb.WORKFLOW_EXECUTION_STATUS_RUNNING, response.StatusWorkflowRunning},
		{"continued as new", enumspb.WORKFLOW_EXECUTION_STATUS_CONTINUED_AS_NEW, response.StatusWorkflowRunning},
		{"completed", enumspb.WORKFLOW_EXECUTION_STATUS_COMPLETED, response.StatusWorkflowCompleted},
		{"failed", enumspb.WORKFLOW_EXECUTION_STATUS_FAILED, response.StatusWorkflowFailed},
		{"canceled", enumspb.WORKFLOW_EXECUTION_STATUS_CANCELED, response.StatusWorkflowCancelled},
		{"terminated", enumspb.WORKFLOW_EXECUTION_STATUS_TERMINATED, response.StatusWorkflowTerminated},
		{"timed out", enumspb.WORKFLOW_EXECUTION_STATUS_TIMED_OUT, response.StatusWorkflowTimedOut},
		{"unknown/unspecified falls back to running", enumspb.WORKFLOW_EXECUTION_STATUS_UNSPECIFIED, response.StatusWorkflowRunning},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotStatus, gotMessage := classifyExistingRun(tt.status)
			if gotStatus != tt.want {
				t.Errorf("classifyExistingRun(%v) status = %q, want %q", tt.status, gotStatus, tt.want)
			}
			if gotMessage == "" {
				t.Error("expected a non-empty message")
			}
			if !gotStatus.IsError() {
				t.Errorf("%q.IsError() = false, want true (attaching to an existing run should never look like a fresh start)", gotStatus)
			}
		})
	}
}
