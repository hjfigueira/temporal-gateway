package spec

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// loadSpec writes yamlContent to a temp file and loads it through Load, so
// tests exercise the real read-expand-parse-validate pipeline rather than
// constructing a Spec value directly.
func loadSpec(t *testing.T, yamlContent string) (*Spec, error) {
	t.Helper()
	return loadSpecFiles(t, yamlContent)
}

// loadSpecFiles writes each of yamlContents to its own temp file, numbered
// in order (so the merge order in the returned Load call is deterministic),
// and loads them all through Load in that same order.
func loadSpecFiles(t *testing.T, yamlContents ...string) (*Spec, error) {
	t.Helper()
	dir := t.TempDir()
	paths := make([]string, len(yamlContents))
	for i, content := range yamlContents {
		path := filepath.Join(dir, fmt.Sprintf("api-spec-%d.yaml", i))
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("failed to write test spec: %v", err)
		}
		paths[i] = path
	}
	return Load(paths...)
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
        triggers:
        - action: startWorkflow
          namespace: default
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

	binding := s.Paths["/widgets"].Post.Temporal.Triggers[0]
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

// widgetTrigger renders a minimal, valid single-trigger x-temporal block for
// a startWorkflow to workflowID, so the merge tests below can build small,
// distinguishable operations without repeating every required field.
func widgetTrigger(workflowID string) string {
	return fmt.Sprintf(`        triggers:
        - action: startWorkflow
          namespace: default
          workflowType: WidgetWorkflow
          workflowId: %q
          taskQueue: widgets-task-queue
`, workflowID)
}

func TestLoadMergesPathsFromMultipleFiles(t *testing.T) {
	base := `openapi: 3.0.3
info:
  title: Test
  version: "1.0.0"
paths:
  /widgets:
    post:
      operationId: createWidget
      x-temporal:
` + widgetTrigger("widget-1")

	extra := `openapi: 3.0.3
info:
  title: Test
  version: "1.0.0"
paths:
  /gadgets:
    post:
      operationId: createGadget
      x-temporal:
` + widgetTrigger("gadget-1")

	s, err := loadSpecFiles(t, base, extra)
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}

	if _, ok := s.Paths["/widgets"]; !ok {
		t.Error("expected /widgets from the first file to survive the merge")
	}
	if _, ok := s.Paths["/gadgets"]; !ok {
		t.Error("expected /gadgets from the second file to be added by the merge")
	}
}

// TestLoadLaterFileSupersedesEarlierOperation checks the core conflict
// rule: when two files declare the same (path, method) operation, the
// later file's definition wins entirely, rather than the two being merged
// field by field.
func TestLoadLaterFileSupersedesEarlierOperation(t *testing.T) {
	first := `openapi: 3.0.3
info:
  title: Test
  version: "1.0.0"
paths:
  /widgets:
    post:
      operationId: createWidgetV1
      x-temporal:
` + widgetTrigger("widget-v1")

	second := `openapi: 3.0.3
info:
  title: Test
  version: "1.0.0"
paths:
  /widgets:
    post:
      operationId: createWidgetV2
      x-temporal:
` + widgetTrigger("widget-v2")

	s, err := loadSpecFiles(t, first, second)
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}

	op := s.Paths["/widgets"].Post
	if op.OperationID != "createWidgetV2" {
		t.Errorf("operationId = %q, want the second file's createWidgetV2 to supersede the first", op.OperationID)
	}
	if got := op.Temporal.Triggers[0].WorkflowID; got != "widget-v2" {
		t.Errorf("workflowId = %q, want the second file's widget-v2 to supersede the first", got)
	}
}

// TestLoadMergesMethodsOnSharedPath checks that a conflict is scoped to the
// (path, method) pair, not the whole path: two files declaring different
// methods on the same path both survive the merge instead of one path item
// wholesale replacing the other.
func TestLoadMergesMethodsOnSharedPath(t *testing.T) {
	getFile := `openapi: 3.0.3
info:
  title: Test
  version: "1.0.0"
paths:
  /widgets/{id}:
    get:
      operationId: getWidget
      x-temporal:
        triggers:
        - action: queryWorkflow
          namespace: default
          workflowId: "widget-{path.id}"
          queryType: getWidgetState
`

	postFile := `openapi: 3.0.3
info:
  title: Test
  version: "1.0.0"
paths:
  /widgets/{id}:
    post:
      operationId: updateWidget
      x-temporal:
` + widgetTrigger("widget-{path.id}")

	s, err := loadSpecFiles(t, getFile, postFile)
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}

	item := s.Paths["/widgets/{id}"]
	if item.Get == nil || item.Get.OperationID != "getWidget" {
		t.Errorf("expected GET /widgets/{id} from the first file to survive, got %+v", item.Get)
	}
	if item.Post == nil || item.Post.OperationID != "updateWidget" {
		t.Errorf("expected POST /widgets/{id} from the second file to be added, got %+v", item.Post)
	}
}

// TestLoadMergesInfoFieldByField checks that a later file's info fields
// override the earlier file's only where actually set, rather than the
// later file's (possibly zero-value) info object replacing the earlier
// one's wholesale.
func TestLoadMergesInfoFieldByField(t *testing.T) {
	base := `openapi: 3.0.3
info:
  title: Base Title
  version: "1.0.0"
  description: original description
paths:
  /widgets:
    post:
      operationId: createWidget
      x-temporal:
` + widgetTrigger("widget-1")

	versionBump := `openapi: 3.0.3
info:
  version: "2.0.0"
paths: {}
`

	s, err := loadSpecFiles(t, base, versionBump)
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}

	if s.Info.Title != "Base Title" {
		t.Errorf("Info.Title = %q, want it preserved from the first file since the second didn't set it", s.Info.Title)
	}
	if s.Info.Version != "2.0.0" {
		t.Errorf("Info.Version = %q, want the second file's 2.0.0 to override the first", s.Info.Version)
	}
	if s.Info.Description != "original description" {
		t.Errorf("Info.Description = %q, want it preserved from the first file since the second didn't set it", s.Info.Description)
	}
}

func TestLoadRejectsNoPaths(t *testing.T) {
	if _, err := Load(); err == nil {
		t.Fatal("expected an error when Load is called with no paths")
	}
}
