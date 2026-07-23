package temporal

import (
	"testing"

	enumspb "go.temporal.io/api/enums/v1"

	"temporal-gateway/internal/response"
)

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
