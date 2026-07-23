package spec

import (
	"fmt"
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
            - name: CustomStringField
              type: string
              value: widget
            - name: CustomIntField
              type: int
              value: 42
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
	if len(binding.SearchAttributes) != 2 {
		t.Fatalf("expected 2 search attributes, got %+v", binding.SearchAttributes)
	}
	if got := binding.SearchAttributes[0]; got.Name != "CustomStringField" || got.Type != "string" || got.Value != "widget" {
		t.Errorf("SearchAttributes[0] not parsed correctly: %+v", got)
	}
	if got := binding.SearchAttributes[1]; got.Name != "CustomIntField" || got.Type != "int" || got.Value != 42 {
		t.Errorf("SearchAttributes[1] not parsed correctly: %+v", got)
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

func TestValidateAllowsTerminateIfRunningAlone(t *testing.T) {
	yamlContent := specHeader + `      x-temporal:
        - action: startWorkflow
          workflowType: WidgetWorkflow
          workflowId: "widget-1"
          taskQueue: widgets-task-queue
          idReusePolicy: TerminateIfRunning
`
	if _, err := loadSpec(t, yamlContent); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestValidateRejectsTerminateIfRunningWithConflictPolicy(t *testing.T) {
	yamlContent := specHeader + `      x-temporal:
        - action: startWorkflow
          workflowType: WidgetWorkflow
          workflowId: "widget-1"
          taskQueue: widgets-task-queue
          idReusePolicy: TerminateIfRunning
          workflowIdConflictPolicy: Fail
`
	_, err := loadSpec(t, yamlContent)
	if err == nil {
		t.Fatal("expected an error combining idReusePolicy TerminateIfRunning with an explicit workflowIdConflictPolicy")
	}
	if !strings.Contains(err.Error(), "TerminateIfRunning") {
		t.Errorf("error %q does not mention TerminateIfRunning", err.Error())
	}
}

func TestValidateRejectsUnknownSearchAttributeType(t *testing.T) {
	yamlContent := specHeader + `      x-temporal:
        - action: startWorkflow
          workflowType: WidgetWorkflow
          workflowId: "widget-1"
          taskQueue: widgets-task-queue
          searchAttributes:
            - name: CustomField
              type: bogus
              value: x
`
	_, err := loadSpec(t, yamlContent)
	if err == nil {
		t.Fatal("expected an error for an unknown searchAttributes type")
	}
	if !strings.Contains(err.Error(), "unknown type") {
		t.Errorf("error %q does not mention the unknown type", err.Error())
	}
}

func TestValidateRejectsSearchAttributeMissingName(t *testing.T) {
	yamlContent := specHeader + `      x-temporal:
        - action: startWorkflow
          workflowType: WidgetWorkflow
          workflowId: "widget-1"
          taskQueue: widgets-task-queue
          searchAttributes:
            - type: string
              value: x
`
	_, err := loadSpec(t, yamlContent)
	if err == nil {
		t.Fatal("expected an error for a searchAttributes entry with no name")
	}
	if !strings.Contains(err.Error(), "missing name") {
		t.Errorf("error %q does not mention the missing name", err.Error())
	}
}

func TestValidateRejectsSearchAttributeValueTypeMismatch(t *testing.T) {
	tests := []struct {
		name   string
		saType string
		value  string
	}{
		{"string type with non-string value", "string", "value: 123"},
		{"bool type with non-bool value", "bool", `value: "yes"`},
		{"int type with non-int value", "int", `value: "1"`},
		{"float type with non-numeric value", "float", `value: "1.5"`},
		{"time type with an invalid timestamp", "time", `value: "not-a-timestamp"`},
		{"keywordList type with a scalar value", "keywordList", `value: "not-a-list"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			yamlContent := specHeader + fmt.Sprintf(`      x-temporal:
        - action: startWorkflow
          workflowType: WidgetWorkflow
          workflowId: "widget-1"
          taskQueue: widgets-task-queue
          searchAttributes:
            - name: CustomField
              type: %s
              %s
`, tt.saType, tt.value)
			if _, err := loadSpec(t, yamlContent); err == nil {
				t.Fatalf("expected an error for a %s search attribute with a mismatched value", tt.saType)
			}
		})
	}
}

func TestValidateAcceptsValidSearchAttributes(t *testing.T) {
	yamlContent := specHeader + `      x-temporal:
        - action: startWorkflow
          workflowType: WidgetWorkflow
          workflowId: "widget-1"
          taskQueue: widgets-task-queue
          searchAttributes:
            - name: CustomStringField
              type: string
              value: widget
            - name: CustomKeywordField
              type: keyword
              value: gold
            - name: CustomBoolField
              type: bool
              value: true
            - name: CustomIntField
              type: int
              value: 42
            - name: CustomFloatField
              type: float
              value: 3.5
            - name: CustomTimeField
              type: time
              value: "2026-01-02T15:04:05Z"
            - name: CustomKeywordListField
              type: keywordList
              value: ["a", "b"]
`
	if _, err := loadSpec(t, yamlContent); err != nil {
		t.Fatalf("expected no error, got %v", err)
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
