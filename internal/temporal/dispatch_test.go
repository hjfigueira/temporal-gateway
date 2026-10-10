package temporal

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"

	"temporal-gateway/internal/config"
	"temporal-gateway/internal/response"
	"temporal-gateway/internal/spec"
)

// newFakeDispatcher dials a real SDK client at a fake frontend and wraps it
// in a Dispatcher whose "default" namespace catalogs OrderWorkflow's default
// task queue.
func newFakeDispatcher(t *testing.T) (*fakeFrontend, *Dispatcher) {
	t.Helper()
	f, addr := startFakeFrontend(t)
	conns := Connections{"default": {
		Client:  dialFake(t, addr),
		Catalog: NewCatalog([]config.WorkflowDefinition{{Name: "OrderWorkflow", TaskQueue: "orders"}}),
	}}
	return f, NewDispatcher(conns)
}

func dispatch(t *testing.T, d *Dispatcher, b spec.TemporalBinding, body any) (any, error) {
	t.Helper()
	if b.Namespace == "" {
		b.Namespace = "default"
	}
	return d.Dispatch(context.Background(), b, "wf-1", body)
}

func TestDispatchActions(t *testing.T) {
	_, d := newFakeDispatcher(t)

	tests := []struct {
		name    string
		binding spec.TemporalBinding
		body    any
		want    any
	}{
		{
			name:    "signal",
			binding: spec.TemporalBinding{Action: spec.ActionSignalWorkflow, SignalName: "approve"},
			body:    map[string]any{"by": "me"},
			want:    response.WorkflowSignaled{Envelope: response.Envelope{Status: response.StatusSignaled}, WorkflowID: "wf-1", SignalName: "approve"},
		},
		{
			name:    "cancel",
			binding: spec.TemporalBinding{Action: spec.ActionCancelWorkflow},
			want:    response.WorkflowAck{Envelope: response.Envelope{Status: response.StatusCancelled}, WorkflowID: "wf-1"},
		},
		{
			name:    "terminate with a reason field",
			binding: spec.TemporalBinding{Action: spec.ActionTerminateWorkflow},
			body:    map[string]any{"reason": "duplicate"},
			want:    response.WorkflowAck{Envelope: response.Envelope{Status: response.StatusTerminated}, WorkflowID: "wf-1"},
		},
		{
			name:    "terminate with a plain-string reason",
			binding: spec.TemporalBinding{Action: spec.ActionTerminateWorkflow},
			body:    "duplicate",
			want:    response.WorkflowAck{Envelope: response.Envelope{Status: response.StatusTerminated}, WorkflowID: "wf-1"},
		},
		{
			name:    "query with an argument",
			binding: spec.TemporalBinding{Action: spec.ActionQueryWorkflow, QueryType: "state"},
			body:    map[string]any{"verbose": true},
			want:    map[string]any{"ok": true},
		},
		{
			name:    "query without an argument",
			binding: spec.TemporalBinding{Action: spec.ActionQueryWorkflow, QueryType: "state"},
			want:    map[string]any{"ok": true},
		},
		{
			name:    "getResult",
			binding: spec.TemporalBinding{Action: spec.ActionGetResult},
			want:    map[string]any{"ok": true},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := dispatch(t, d, tt.binding, tt.body)
			if err != nil {
				t.Fatalf("Dispatch: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("Dispatch = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestDispatchActionErrors(t *testing.T) {
	f, d := newFakeDispatcher(t)
	f.set(func(f *fakeFrontend) { f.err = serviceerror.NewNotFound("no such workflow") })

	for _, b := range []spec.TemporalBinding{
		{Action: spec.ActionStartWorkflow, WorkflowType: "OrderWorkflow"},
		{Action: spec.ActionSignalWorkflow, SignalName: "s"},
		{Action: spec.ActionCancelWorkflow},
		{Action: spec.ActionTerminateWorkflow},
		{Action: spec.ActionQueryWorkflow, QueryType: "q"},
		{Action: spec.ActionGetResult},
	} {
		t.Run(string(b.Action), func(t *testing.T) {
			if _, err := dispatch(t, d, b, nil); err == nil {
				t.Fatal("Dispatch returned nil error, want the server's NotFound")
			}
		})
	}
}

func TestDispatchResultDecodingErrors(t *testing.T) {
	f, d := newFakeDispatcher(t)
	undecodable := &commonpb.Payloads{Payloads: []*commonpb.Payload{{
		Metadata: map[string][]byte{"encoding": []byte("json/plain")},
		Data:     []byte("{not json"),
	}}}
	f.set(func(f *fakeFrontend) { f.result = undecodable })

	if _, err := dispatch(t, d, spec.TemporalBinding{Action: spec.ActionQueryWorkflow, QueryType: "q"}, nil); err == nil {
		t.Error("query with an undecodable result: want an error")
	}

	f.set(func(f *fakeFrontend) { f.failWorkflow = true })
	if _, err := dispatch(t, d, spec.TemporalBinding{Action: spec.ActionGetResult}, nil); err == nil {
		t.Error("getResult on a terminated workflow: want an error")
	}
}

func TestDispatchRejectsUnknownNamespaceAndAction(t *testing.T) {
	_, d := newFakeDispatcher(t)
	if _, err := dispatch(t, d, spec.TemporalBinding{Action: spec.ActionCancelWorkflow, Namespace: "elsewhere"}, nil); err == nil || !strings.Contains(err.Error(), `"elsewhere"`) {
		t.Errorf("unknown namespace: err = %v", err)
	}
	if _, err := dispatch(t, d, spec.TemporalBinding{Action: "explode"}, nil); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Errorf("unknown action: err = %v", err)
	}
}

func TestStartWorkflowAppliesEveryOption(t *testing.T) {
	f, d := newFakeDispatcher(t)
	binding := spec.TemporalBinding{
		Action:                   spec.ActionStartWorkflow,
		WorkflowType:             "OrderWorkflow", // no taskQueue: falls back to the catalog's
		IDReusePolicy:            "RejectDuplicate",
		WorkflowIDConflictPolicy: "Fail",
		WorkflowExecutionTimeout: "24h",
		WorkflowRunTimeout:       "1h",
		WorkflowTaskTimeout:      "10s",
		StartDelay:               "5s",
		RetryPolicy:              &spec.RetryPolicy{InitialInterval: "1s", BackoffCoefficient: 2, MaximumInterval: "1m", MaximumAttempts: 3},
		Priority:                 &spec.Priority{PriorityKey: 2},
		SearchAttributes:         []spec.SearchAttribute{{Name: "CustomKeywordField", Type: "keyword", Value: "x"}},
	}

	got, err := dispatch(t, d, binding, map[string]any{"orderId": "o1"})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	want := response.WorkflowStarted{Envelope: response.Envelope{Status: response.StatusStarted}, WorkflowID: "wf-1", RunID: "run-1"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Dispatch = %#v, want %#v", got, want)
	}

	f.mu.Lock()
	req := f.lastStart
	f.mu.Unlock()
	checks := map[string]bool{
		"task queue from catalog": req.GetTaskQueue().GetName() == "orders",
		"reuse policy":            req.GetWorkflowIdReusePolicy() == enumspb.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
		"conflict policy":         req.GetWorkflowIdConflictPolicy() == enumspb.WORKFLOW_ID_CONFLICT_POLICY_FAIL,
		"execution timeout":       req.GetWorkflowExecutionTimeout().AsDuration() == 24*time.Hour,
		"run timeout":             req.GetWorkflowRunTimeout().AsDuration() == time.Hour,
		"task timeout":            req.GetWorkflowTaskTimeout().AsDuration() == 10*time.Second,
		"start delay":             req.GetWorkflowStartDelay().AsDuration() == 5*time.Second,
		"retry policy":            req.GetRetryPolicy().GetMaximumAttempts() == 3,
		"priority":                req.GetPriority().GetPriorityKey() == 2,
		"search attributes":       len(req.GetSearchAttributes().GetIndexedFields()) == 1,
		"input":                   len(req.GetInput().GetPayloads()) == 1,
	}
	for name, ok := range checks {
		if !ok {
			t.Errorf("start request: %s not applied (%v)", name, req)
		}
	}
}

func TestStartWorkflowRejectsBadSearchAttribute(t *testing.T) {
	_, d := newFakeDispatcher(t)
	b := spec.TemporalBinding{Action: spec.ActionStartWorkflow, WorkflowType: "W", TaskQueue: "q",
		SearchAttributes: []spec.SearchAttribute{{Name: "N", Type: "int", Value: "not an int"}}}
	if _, err := dispatch(t, d, b, nil); err == nil || !strings.Contains(err.Error(), "searchAttributes") {
		t.Fatalf("err = %v, want a searchAttributes error", err)
	}
}

func TestStartWorkflowReportsAttachedRunState(t *testing.T) {
	f, d := newFakeDispatcher(t)
	b := spec.TemporalBinding{Action: spec.ActionStartWorkflow, WorkflowType: "W", TaskQueue: "q"}

	f.set(func(f *fakeFrontend) {
		f.started = false
		f.describeStatus = enumspb.WORKFLOW_EXECUTION_STATUS_COMPLETED
	})
	got, err := dispatch(t, d, b, nil)
	if err != nil {
		t.Fatal(err)
	}
	if s := got.(response.WorkflowStarted).Status; s != response.StatusWorkflowCompleted {
		t.Errorf("attached to a completed run: status = %q, want %q", s, response.StatusWorkflowCompleted)
	}

	f.set(func(f *fakeFrontend) { f.describeErr = serviceerror.NewPermissionDenied("describe denied", "") })
	got, err = dispatch(t, d, b, nil)
	if err != nil {
		t.Fatal(err)
	}
	if s := got.(response.WorkflowStarted).Status; s != response.StatusWorkflowRunning {
		t.Errorf("describe failed: status = %q, want fallback %q", s, response.StatusWorkflowRunning)
	}
}
