package gateway

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"temporal-gateway/internal/response"
	"temporal-gateway/internal/spec"
)

// capturingDispatcher records the single binding/workflowID/body it was
// asked to dispatch, so nexus driver tests can inspect exactly what got
// sent to Temporal without a real client.
type capturingDispatcher struct {
	mu         sync.Mutex
	binding    spec.TemporalBinding
	workflowID string
	body       any
	calls      int
}

func (d *capturingDispatcher) Dispatch(_ context.Context, binding spec.TemporalBinding, workflowID string, body any) (any, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.binding = binding
	d.workflowID = workflowID
	d.body = body
	d.calls++
	return response.WorkflowStarted{
		Envelope:   response.Envelope{Status: response.StatusStarted},
		WorkflowID: workflowID,
		RunID:      "run-1",
	}, nil
}

func nexusRoute() spec.Route {
	return spec.Route{
		Method: "POST",
		Path:   "/orders",
		Operation: &spec.Operation{
			OperationID: "createOrder",
			Temporal: spec.TemporalSpec{
				Driver: spec.DriverNexus,
				Config: &spec.NexusConfig{
					Namespace:  "cascade",
					TaskQueue:  "cascade-task-queue",
					WorkflowID: "cascade-{body.orderId}",
				},
				Triggers: []spec.TemporalBinding{
					{Action: spec.ActionStartWorkflow, Namespace: "default", WorkflowType: "OrderWorkflow", WorkflowID: "order-{body.orderId}"},
					{Action: spec.ActionStartWorkflow, Namespace: "notifications", WorkflowType: "NotificationWorkflow", WorkflowID: "order-{body.orderId}-notification"},
				},
			},
		},
	}
}

// TestNexusDriverStartsOneCascadeWorkflow checks the core behavior the
// nexus driver adds: instead of dispatching each trigger straight to
// Temporal (as the direct driver would - two Dispatch calls), it makes
// exactly one Dispatch call, for a synthetic startWorkflow binding built
// from x-temporal.config, with the triggers (rendered against the request)
// carried as the workflow's input.
func TestNexusDriverStartsOneCascadeWorkflow(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	dispatcher := &capturingDispatcher{}
	driver := NexusDriver{
		Dispatcher:     dispatcher,
		NexusEndpoints: map[string]string{"default": "default-endpoint", "notifications": "notifications-endpoint"},
	}

	handler := driver.Handler(nexusRoute(), logger)

	req := httptest.NewRequest(http.MethodPost, "/orders", strings.NewReader(`{"orderId":"o1"}`))
	rec := httptest.NewRecorder()
	handler(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d (body: %s)", rec.Code, http.StatusAccepted, rec.Body.String())
	}

	if dispatcher.calls != 1 {
		t.Fatalf("dispatcher was called %d times, want exactly 1 (one cascade workflow, not one per trigger)", dispatcher.calls)
	}

	if dispatcher.binding.Action != spec.ActionStartWorkflow {
		t.Errorf("binding.Action = %q, want %q", dispatcher.binding.Action, spec.ActionStartWorkflow)
	}
	if dispatcher.binding.Namespace != "cascade" {
		t.Errorf("binding.Namespace = %q, want %q", dispatcher.binding.Namespace, "cascade")
	}
	if dispatcher.binding.WorkflowType != spec.DefaultCascadeWorkflowType {
		t.Errorf("binding.WorkflowType = %q, want %q", dispatcher.binding.WorkflowType, spec.DefaultCascadeWorkflowType)
	}
	if dispatcher.workflowID != "cascade-o1" {
		t.Errorf("workflowID = %q, want %q", dispatcher.workflowID, "cascade-o1")
	}

	input, ok := dispatcher.body.(spec.CascadeInput)
	if !ok {
		t.Fatalf("body = %#v (%T), want spec.CascadeInput", dispatcher.body, dispatcher.body)
	}
	if len(input.Triggers) != 2 {
		t.Fatalf("got %d triggers, want 2", len(input.Triggers))
	}
	if input.Triggers[0].WorkflowID != "order-o1" {
		t.Errorf("triggers[0].WorkflowID = %q, want %q (templating should have run before reaching CascadeEvent)", input.Triggers[0].WorkflowID, "order-o1")
	}
	if input.Triggers[0].NexusEndpoint != "default-endpoint" {
		t.Errorf("triggers[0].NexusEndpoint = %q, want %q", input.Triggers[0].NexusEndpoint, "default-endpoint")
	}
	if input.Triggers[1].WorkflowID != "order-o1-notification" {
		t.Errorf("triggers[1].WorkflowID = %q, want %q", input.Triggers[1].WorkflowID, "order-o1-notification")
	}
	if input.Triggers[1].NexusEndpoint != "notifications-endpoint" {
		t.Errorf("triggers[1].NexusEndpoint = %q, want %q", input.Triggers[1].NexusEndpoint, "notifications-endpoint")
	}

	var item map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &item); err != nil {
		t.Fatalf("response body is not a JSON object: %v (body: %s)", err, rec.Body.String())
	}
	if item["status"] != string(response.StatusStarted) {
		t.Errorf("status = %v, want %q", item["status"], response.StatusStarted)
	}
	if item["workflowId"] != "cascade-o1" {
		t.Errorf("workflowId = %v, want %q (the cascade workflow's own id, not a trigger's)", item["workflowId"], "cascade-o1")
	}
}

// TestNewHandlerSelectsDriverPerOperation checks that NewHandler routes a
// direct-driver operation and a nexus-driver operation to their respective
// Driver, per x-temporal.driver, within the same spec.
func TestNewHandlerSelectsDriverPerOperation(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	directDispatcher := &capturingDispatcher{}
	nexusDispatcher := &capturingDispatcher{}

	apiSpec := &spec.Spec{
		Paths: map[string]spec.PathItem{
			"/direct": {Post: &spec.Operation{
				OperationID: "directOp",
				Temporal: spec.TemporalSpec{
					Triggers: []spec.TemporalBinding{
						{Action: spec.ActionStartWorkflow, Namespace: "default", WorkflowType: "OrderWorkflow", WorkflowID: "order-1"},
					},
				},
			}},
			"/cascade": {Post: nexusRoute().Operation},
		},
	}

	handler := NewHandler(apiSpec,
		DirectDriver{Dispatcher: directDispatcher},
		NexusDriver{Dispatcher: nexusDispatcher, NexusEndpoints: map[string]string{"default": "default-endpoint", "notifications": "notifications-endpoint"}},
		logger,
	)

	req := httptest.NewRequest(http.MethodPost, "/direct", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if directDispatcher.calls != 1 {
		t.Errorf("direct route: dispatcher.calls = %d, want 1", directDispatcher.calls)
	}
	if nexusDispatcher.calls != 0 {
		t.Errorf("direct route: nexusDispatcher.calls = %d, want 0", nexusDispatcher.calls)
	}

	req = httptest.NewRequest(http.MethodPost, "/cascade", strings.NewReader(`{"orderId":"o1"}`))
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if nexusDispatcher.calls != 1 {
		t.Errorf("cascade route: nexusDispatcher.calls = %d, want 1", nexusDispatcher.calls)
	}
	if directDispatcher.calls != 1 {
		t.Errorf("cascade route: directDispatcher.calls = %d, want still 1 (unaffected by the other route)", directDispatcher.calls)
	}
}
