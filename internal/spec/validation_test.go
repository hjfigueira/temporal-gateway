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

func TestValidateReturnsAllErrorsAtOnce(t *testing.T) {
	// Two independent problems on two different bindings: neither should
	// mask the other.
	yamlContent := specHeader + `      x-temporal:
        triggers:
        - action: startWorkflow
          taskQueue: widgets
          workflowId: "widget-1"
        - action: signalWorkflow
          workflowId: "widget-1"
`
	_, err := loadSpec(t, yamlContent)
	if err == nil {
		t.Fatal("expected an error")
	}

	wantSubstrings := []string{"workflowType", "signalName"}
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

func TestValidateWorkflowIDPlaceholders(t *testing.T) {
	tests := []struct {
		name       string
		path       string
		workflowID string
		wantErr    string
	}{
		{name: "every origin and uuidv7", path: "/orders/{orderId}", workflowID: "o-{path.orderId}-{body.a}-{query.b}-{HEADER.X-C}-{UUIDv7}"},
		{name: "unknown path param", path: "/orders/{orderId}", workflowID: "o-{path.id}", wantErr: "names no path parameter"},
		{name: "typo'd origin", path: "/orders", workflowID: "o-{paht.id}", wantErr: `unknown origin "paht"`},
		{name: "missing origin", path: "/orders", workflowID: "o-{orderId}", wantErr: "must be {uuidv7}, {origin.field}, or {fingerprint(...)}"},
		{name: "empty field", path: "/orders", workflowID: "o-{body.}", wantErr: "empty field name"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := validateWorkflowIDTemplate("POST", tt.path, 0, tt.workflowID)
			if tt.wantErr == "" {
				if len(errs) != 0 {
					t.Fatalf("want no errors, got %v", errs)
				}
				return
			}
			if len(errs) != 1 || !strings.Contains(errs[0].Error(), tt.wantErr) {
				t.Fatalf("errs = %v, want one containing %q", errs, tt.wantErr)
			}
		})
	}
}

func TestValidateRejectsBadRequestSchemas(t *testing.T) {
	// kin-openapi stops at the first problem inside one schema, so each
	// mistake gets its own spec.
	tests := []struct{ schema, want string }{
		{`{type: strnig}`, `requestBody: unsupported 'type' value "strnig"`},
		{`{type: string, pattern: "(["}`, "requestBody: error parsing regexp"},
	}
	for _, tt := range tests {
		yamlContent := specHeader + `      parameters:
        - {name: limit, in: query, schema: {type: integr}}
      requestBody:
        content:
          application/json:
            schema:
              type: object
              properties:
                sku: ` + tt.schema + `
      x-temporal:
        triggers:
        - action: getResult
          namespace: default
          workflowId: "widget-1"
`
		_, err := loadSpec(t, yamlContent)
		if err == nil {
			t.Fatalf("%s: expected an error", tt.schema)
		}
		for _, want := range []string{tt.want, `parameter "limit" schema is invalid: unsupported 'type' value "integr"`} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%s: error %q does not mention %q", tt.schema, err, want)
			}
		}
	}
}
