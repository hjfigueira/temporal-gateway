package spec

import (
	"fmt"
	"strings"
	"testing"
)

func TestValidateRejectsBadDuration(t *testing.T) {
	yamlContent := specHeader + `      x-temporal:
        triggers:
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
        triggers:
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
        triggers:
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
        triggers:
        - action: startWorkflow
          namespace: default
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
        triggers:
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
        triggers:
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
        triggers:
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
        triggers:
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
        triggers:
        - action: startWorkflow
          namespace: default
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
        triggers:
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
        triggers:
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

func TestValidateAcceptsKnownReturnStrategies(t *testing.T) {
	for _, strategy := range []string{"", "acceptPartial", "allOrNothing"} {
		t.Run(strategy, func(t *testing.T) {
			yamlContent := specHeader + fmt.Sprintf(`      x-temporal:
        returnStrategy: %s
        triggers:
        - action: startWorkflow
          namespace: default
          workflowType: WidgetWorkflow
          workflowId: "widget-1"
          taskQueue: widgets-task-queue
`, strategy)
			if _, err := loadSpec(t, yamlContent); err != nil {
				t.Fatalf("expected no error for returnStrategy %q, got %v", strategy, err)
			}
		})
	}
}

func TestValidateRejectsUnknownReturnStrategy(t *testing.T) {
	yamlContent := specHeader + `      x-temporal:
        returnStrategy: bogus
        triggers:
        - action: startWorkflow
          namespace: default
          workflowType: WidgetWorkflow
          workflowId: "widget-1"
          taskQueue: widgets-task-queue
`
	_, err := loadSpec(t, yamlContent)
	if err == nil {
		t.Fatal("expected an error for an unknown returnStrategy")
	}
	if !strings.Contains(err.Error(), "returnStrategy") {
		t.Errorf("error %q does not mention returnStrategy", err.Error())
	}
}

func TestValidateRejectsMissingTriggers(t *testing.T) {
	yamlContent := specHeader + `      x-temporal:
        returnStrategy: acceptPartial
`
	_, err := loadSpec(t, yamlContent)
	if err == nil {
		t.Fatal("expected an error for x-temporal with no triggers")
	}
	if !strings.Contains(err.Error(), "triggers") {
		t.Errorf("error %q does not mention triggers", err.Error())
	}
}

func TestValidateAcceptsKnownDrivers(t *testing.T) {
	for _, driver := range []string{"", "direct"} {
		t.Run(driver, func(t *testing.T) {
			yamlContent := specHeader + fmt.Sprintf(`      x-temporal:
        driver: %s
        triggers:
        - action: startWorkflow
          namespace: default
          workflowType: WidgetWorkflow
          workflowId: "widget-1"
          taskQueue: widgets-task-queue
`, driver)
			if _, err := loadSpec(t, yamlContent); err != nil {
				t.Fatalf("expected no error for driver %q, got %v", driver, err)
			}
		})
	}
}

func TestValidateRejectsUnknownDriver(t *testing.T) {
	yamlContent := specHeader + `      x-temporal:
        driver: bogus
        triggers:
        - action: startWorkflow
          namespace: default
          workflowType: WidgetWorkflow
          workflowId: "widget-1"
          taskQueue: widgets-task-queue
`
	_, err := loadSpec(t, yamlContent)
	if err == nil {
		t.Fatal("expected an error for an unknown driver")
	}
	if !strings.Contains(err.Error(), "driver") {
		t.Errorf("error %q does not mention driver", err.Error())
	}
}

func TestValidateRejectsNexusDriverWithoutConfig(t *testing.T) {
	yamlContent := specHeader + `      x-temporal:
        driver: nexus
        triggers:
        - action: startWorkflow
          namespace: default
          workflowType: WidgetWorkflow
          workflowId: "widget-1"
          taskQueue: widgets-task-queue
`
	_, err := loadSpec(t, yamlContent)
	if err == nil {
		t.Fatal("expected an error for driver nexus with no config")
	}
	if !strings.Contains(err.Error(), "config") {
		t.Errorf("error %q does not mention config", err.Error())
	}
}

func TestValidateRejectsIncompleteNexusConfig(t *testing.T) {
	yamlContent := specHeader + `      x-temporal:
        driver: nexus
        config:
          namespace: cascade
        triggers:
        - action: startWorkflow
          namespace: default
          workflowType: WidgetWorkflow
          workflowId: "widget-1"
          taskQueue: widgets-task-queue
`
	_, err := loadSpec(t, yamlContent)
	if err == nil {
		t.Fatal("expected an error for a config missing taskQueue/workflowId")
	}
	if !strings.Contains(err.Error(), "taskQueue") || !strings.Contains(err.Error(), "workflowId") {
		t.Errorf("error %q does not mention both missing fields", err.Error())
	}
}

func TestValidateRejectsConfigWithoutNexusDriver(t *testing.T) {
	yamlContent := specHeader + `      x-temporal:
        config:
          namespace: cascade
          taskQueue: cascade-task-queue
          workflowId: "cascade-1"
        triggers:
        - action: startWorkflow
          namespace: default
          workflowType: WidgetWorkflow
          workflowId: "widget-1"
          taskQueue: widgets-task-queue
`
	_, err := loadSpec(t, yamlContent)
	if err == nil {
		t.Fatal("expected an error for config set without driver: nexus")
	}
	if !strings.Contains(err.Error(), "config") {
		t.Errorf("error %q does not mention config", err.Error())
	}
}

func TestValidateAcceptsCompleteNexusDriver(t *testing.T) {
	yamlContent := specHeader + `      x-temporal:
        driver: nexus
        config:
          namespace: cascade
          taskQueue: cascade-task-queue
          workflowId: "cascade-{body.orderId}"
        triggers:
        - action: startWorkflow
          namespace: default
          workflowType: WidgetWorkflow
          workflowId: "widget-1"
          taskQueue: widgets-task-queue
`
	spec, err := loadSpec(t, yamlContent)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	op := spec.Paths["/widgets"].Post
	if op.Temporal.DriverOrDefault() != DriverNexus {
		t.Fatalf("driver = %q, want %q", op.Temporal.DriverOrDefault(), DriverNexus)
	}
	if got := op.Temporal.Config.WorkflowTypeOrDefault(); got != DefaultCascadeWorkflowType {
		t.Errorf("WorkflowTypeOrDefault() = %q, want %q", got, DefaultCascadeWorkflowType)
	}
}
