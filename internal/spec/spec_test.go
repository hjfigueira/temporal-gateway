package spec

import (
	"os"
	"path/filepath"
	"testing"
)

// loadSpec writes yamlContent to a temp file and loads it through Load, so
// tests exercise the real read-expand-parse-validate pipeline rather than
// constructing a Spec value directly.
func loadSpec(t *testing.T, yamlContent string) (*Spec, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "api-spec.yaml")
	if err := os.WriteFile(path, []byte(yamlContent), 0o644); err != nil {
		t.Fatalf("failed to write test spec: %v", err)
	}
	return Load(path)
}

// specHeader is a minimal valid OpenAPI document; tests append their own
// "      x-temporal: ..." block (indented to sit under the createWidget
// POST operation) to exercise one binding at a time.
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
