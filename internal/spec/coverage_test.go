package spec

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const allMethodsSpec = `openapi: 3.0.3
info: {title: T, version: "1"}
paths:
  /b:
    get: &op
      x-temporal:
        triggers:
          - {action: cancelWorkflow, namespace: default, workflowId: "w"}
    post: *op
    put: *op
    patch: *op
    delete: *op
  /a:
    get: *op
`

func TestRoutesAreSortedByPathThenMethod(t *testing.T) {
	s, err := loadSpecFiles(t, allMethodsSpec, allMethodsSpec)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	var got []string
	for _, r := range s.Routes() {
		got = append(got, r.Method+" "+r.Path)
	}
	want := []string{"GET /a", "DELETE /b", "GET /b", "PATCH /b", "POST /b", "PUT /b"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Routes() = %v, want %v", got, want)
	}
}

func TestStrategyDefaultsToAcceptPartial(t *testing.T) {
	if got := (TemporalSpec{}).Strategy(); got != ReturnStrategyAcceptPartial {
		t.Errorf("Strategy() = %q, want %q", got, ReturnStrategyAcceptPartial)
	}
	if got := (TemporalSpec{ReturnStrategy: ReturnStrategyAllOrNothing}).Strategy(); got != ReturnStrategyAllOrNothing {
		t.Errorf("Strategy() = %q, want %q", got, ReturnStrategyAllOrNothing)
	}
}

func TestPathParamNamesIgnoresUnclosedBrace(t *testing.T) {
	if got := PathParamNames("/orders/{id}/{broken"); !reflect.DeepEqual(got, []string{"id"}) {
		t.Fatalf("PathParamNames = %v, want [id]", got)
	}
}

func TestLoadFileErrors(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	tests := []struct {
		name    string
		path    string
		wantErr string
	}{
		{name: "missing file", path: filepath.Join(dir, "nope.yaml"), wantErr: "read"},
		{name: "unset env var", path: write("env.yaml", "openapi: ${SPEC_TEST_UNSET_VAR}"), wantErr: "SPEC_TEST_UNSET_VAR"},
		{name: "malformed yaml", path: write("bad.yaml", "paths: ["), wantErr: "parse"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Load(tt.path); err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Load err = %v, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

func TestValidateBindingRules(t *testing.T) {
	tests := []struct {
		name    string
		binding TemporalBinding
		wantErr string
	}{
		{name: "missing action", binding: TemporalBinding{Namespace: "d", WorkflowID: "w"}, wantErr: "missing action"},
		{name: "unknown action", binding: TemporalBinding{Action: "explode", Namespace: "d", WorkflowID: "w"}, wantErr: "unknown action"},
		{name: "query without queryType", binding: TemporalBinding{Action: ActionQueryWorkflow, Namespace: "d", WorkflowID: "w"}, wantErr: "requires queryType"},
		{name: "missing workflowId", binding: TemporalBinding{Action: ActionGetResult, Namespace: "d"}, wantErr: "missing workflowId"},
		{name: "start without workflowType", binding: TemporalBinding{Action: ActionStartWorkflow, Namespace: "d", WorkflowID: "w", TaskQueue: "q"}, wantErr: "requires workflowType"},
		{name: "unknown reuse policy", binding: TemporalBinding{Action: ActionStartWorkflow, Namespace: "d", WorkflowID: "w", WorkflowType: "W", TaskQueue: "q", IDReusePolicy: "Sometimes"}, wantErr: "unknown idReusePolicy"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := validateBinding("POST", "/x", 0, tt.binding)
			if len(errs) == 0 || !strings.Contains(errs[0].Error(), tt.wantErr) {
				t.Fatalf("errs = %v, want first to contain %q", errs, tt.wantErr)
			}
		})
	}
}

func TestValidateSearchAttributeValueRejectsWrongShapes(t *testing.T) {
	for _, sa := range []SearchAttribute{
		{Type: "time", Value: 5},
		{Type: "keywordList", Value: "a"},
		{Type: "keywordList", Value: []any{"a", 1}},
	} {
		if err := validateSearchAttributeValue(sa); err == nil {
			t.Errorf("validateSearchAttributeValue(%+v) = nil, want an error", sa)
		}
	}
}
