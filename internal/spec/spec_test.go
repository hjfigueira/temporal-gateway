package spec

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func loadSpec(t *testing.T, yamlContent string) (*Spec, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "api-spec.yaml")
	if err := os.WriteFile(path, []byte(yamlContent), 0o644); err != nil {
		t.Fatalf("failed to write test spec: %v", err)
	}
	return Load(path)
}

const specHeader = `openapi: 3.0.3
info:
  title: Test
  version: "1.0.0"
paths:
  /widgets:
    post:
      operationId: createWidget
`

func TestLoadStartWorkflowOptions(t *testing.T) {
	yamlContent := specHeader + `      x-temporal:
        - action: startWorkflow
          workflowType: WidgetWorkflow
          workflowId: "widget-1"
          taskQueue: widgets-task-queue
          idReusePolicy: RejectDuplicate
          workflowIdConflictPolicy: UseExisting
          workflowExecutionTimeout: 24h
          workflowRunTimeout: 1h
          workflowTaskTimeout: 10s
          workflowExecutionErrorWhenAlreadyStarted: true
          cronSchedule: "0 * * * *"
          memo:
            team: platform
          searchAttributes:
            CustomStringField: widget
          enableEagerStart: true
          staticSummary: "Widget creation"
          staticDetails: "Created via gateway"
          retryPolicy:
            initialInterval: 1s
            backoffCoefficient: 2.0
            maximumInterval: 1m
            maximumAttempts: 5
            nonRetryableErrorTypes:
              - InvalidWidget
          priority:
            priorityKey: 2
            fairnessKey: tenant-a
            fairnessWeight: 1.5
`
	s, err := loadSpec(t, yamlContent)
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}

	binding := s.Paths["/widgets"].Post.Temporal[0]
	if binding.WorkflowIDConflictPolicy != "UseExisting" {
		t.Errorf("WorkflowIDConflictPolicy = %q, want UseExisting", binding.WorkflowIDConflictPolicy)
	}
	if binding.RetryPolicy == nil || binding.RetryPolicy.MaximumAttempts != 5 {
		t.Errorf("RetryPolicy not parsed correctly: %+v", binding.RetryPolicy)
	}
	if binding.Priority == nil || binding.Priority.PriorityKey != 2 {
		t.Errorf("Priority not parsed correctly: %+v", binding.Priority)
	}
	if binding.Memo["team"] != "platform" {
		t.Errorf("Memo not parsed correctly: %+v", binding.Memo)
	}
}

func TestValidateRejectsBadDuration(t *testing.T) {
	yamlContent := specHeader + `      x-temporal:
        - action: startWorkflow
          workflowType: WidgetWorkflow
          workflowId: "widget-1"
          workflowExecutionTimeout: "not-a-duration"
`
	if _, err := loadSpec(t, yamlContent); err == nil {
		t.Fatal("expected an error for an invalid workflowExecutionTimeout")
	}
}

func TestValidateRejectsUnknownConflictPolicy(t *testing.T) {
	yamlContent := specHeader + `      x-temporal:
        - action: startWorkflow
          workflowType: WidgetWorkflow
          workflowId: "widget-1"
          workflowIdConflictPolicy: "Bogus"
`
	if _, err := loadSpec(t, yamlContent); err == nil {
		t.Fatal("expected an error for an unknown workflowIdConflictPolicy")
	}
}

func TestValidateRejectsCronAndStartDelayTogether(t *testing.T) {
	yamlContent := specHeader + `      x-temporal:
        - action: startWorkflow
          workflowType: WidgetWorkflow
          workflowId: "widget-1"
          cronSchedule: "0 * * * *"
          startDelay: "30s"
`
	if _, err := loadSpec(t, yamlContent); err == nil {
		t.Fatal("expected an error when cronSchedule and startDelay are both set")
	}
}

func TestValidateRejectsMissingTaskQueue(t *testing.T) {
	yamlContent := specHeader + `      x-temporal:
        - action: startWorkflow
          workflowType: WidgetWorkflow
          workflowId: "widget-1"
`
	_, err := loadSpec(t, yamlContent)
	if err == nil {
		t.Fatal("expected an error for a startWorkflow binding with no taskQueue")
	}
	if !strings.Contains(err.Error(), "taskQueue") {
		t.Errorf("error %q does not mention the missing taskQueue", err.Error())
	}
}

func TestValidateReturnsAllErrorsAtOnce(t *testing.T) {
	// Two independent problems on two different bindings: neither should
	// mask the other.
	yamlContent := specHeader + `      x-temporal:
        - action: startWorkflow
          workflowType: WidgetWorkflow
          workflowId: "widget-1"
        - action: signalWorkflow
          workflowId: "widget-1"
`
	_, err := loadSpec(t, yamlContent)
	if err == nil {
		t.Fatal("expected an error")
	}

	wantSubstrings := []string{"taskQueue", "signalName"}
	for _, want := range wantSubstrings {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q is missing expected substring %q; validation should report every problem, not just the first", err.Error(), want)
		}
	}
}
