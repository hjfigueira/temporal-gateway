package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"temporal-gateway/internal/spec"
	"temporal-gateway/internal/validate"
)

func TestRenderTemplateSources(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/orders/o1?region=eu", strings.NewReader(""))
	req.Header.Set("X-Request-Id", "req-123")

	pathParams := map[string]string{"orderId": "o1"}
	body := map[string]any{"customerId": "c1", "priority": float64(2)}

	resolve := fieldResolver(req, pathParams, body)

	tests := []struct {
		name string
		tmpl string
		want string
	}{
		{"path origin", "order-{path.orderId}", "order-o1"},
		{"body origin string", "customer-{body.customerId}", "customer-c1"},
		{"body origin number", "priority-{body.priority}", "priority-2"},
		{"query origin", "region-{query.region}", "region-eu"},
		{"header origin", "trace-{header.X-Request-Id}", "trace-req-123"},
		{"header origin lowercase", "trace-{header.x-request-id}", "trace-req-123"},
		{"unresolved field left untouched", "unknown-{path.missing}", "unknown-{path.missing}"},
		{"unknown origin left untouched", "unknown-{env.missing}", "unknown-{env.missing}"},
		{"missing origin left untouched", "bare-{orderId}", "bare-{orderId}"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := renderTemplate(tt.tmpl, resolve)
			if got != tt.want {
				t.Errorf("renderTemplate(%q) = %q, want %q", tt.tmpl, got, tt.want)
			}
		})
	}
}

func TestRenderTemplateUUIDv7(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/orders", strings.NewReader(""))
	resolve := fieldResolver(req, nil, nil)

	got := renderTemplate("order-{uuidv7}", resolve)
	id := strings.TrimPrefix(got, "order-")

	parsed, err := uuid.Parse(id)
	if err != nil {
		t.Fatalf("generated id %q is not a valid uuid: %v", id, err)
	}
	if parsed.Version() != 7 {
		t.Errorf("generated id %q has version %d, want 7", id, parsed.Version())
	}

	// Case-insensitive keyword.
	if got3 := renderTemplate("{UUIDv7}", resolve); got3 == "{UUIDv7}" {
		t.Errorf("UUIDv7 keyword should be case-insensitive, got %q", got3)
	}

	// A fresh value is generated per occurrence.
	first := renderTemplate("{uuidv7}", resolve)
	second := renderTemplate("{uuidv7}", resolve)
	if first == second {
		t.Errorf("expected distinct uuids per render, got %q twice", first)
	}
}

// recordingDispatcher implements Dispatcher and records whether it was
// invoked, so tests can assert that an invalid request never reaches it.
type recordingDispatcher struct {
	called bool
}

func (d *recordingDispatcher) Dispatch(ctx context.Context, binding spec.TemporalBinding, workflowID string, body any) (any, error) {
	d.called = true
	return map[string]string{"workflowId": workflowID}, nil
}

// trackingDispatcher records, under a mutex, every workflowID it was asked
// to dispatch, and fails dispatch for any workflowID listed in failIDs.
// Used to prove that one binding failing doesn't stop another from being
// attempted.
type trackingDispatcher struct {
	failIDs map[string]bool

	mu   sync.Mutex
	seen map[string]bool
}

func (d *trackingDispatcher) Dispatch(ctx context.Context, binding spec.TemporalBinding, workflowID string, body any) (any, error) {
	d.mu.Lock()
	if d.seen == nil {
		d.seen = map[string]bool{}
	}
	d.seen[workflowID] = true
	d.mu.Unlock()

	if d.failIDs[workflowID] {
		return nil, fmt.Errorf("dispatch failed for %s", workflowID)
	}
	return map[string]string{"workflowId": workflowID}, nil
}

func (d *trackingDispatcher) wasDispatched(workflowID string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.seen[workflowID]
}

func twoBindingRoute() spec.Route {
	return spec.Route{
		Method: "POST",
		Path:   "/orders",
		Operation: &spec.Operation{
			OperationID: "createOrder",
			Temporal: []spec.TemporalBinding{
				{Action: spec.ActionStartWorkflow, WorkflowType: "OrderWorkflow", WorkflowID: "order-{body.orderId}"},
				{Action: spec.ActionStartWorkflow, WorkflowType: "NotificationWorkflow", WorkflowID: "order-{body.orderId}-notification"},
			},
		},
	}
}

func TestDispatchHandlerRunsAllBindingsEvenWhenOneFails(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	t.Run("both bindings dispatch when all succeed", func(t *testing.T) {
		dispatcher := &trackingDispatcher{}
		handler := dispatchHandler(twoBindingRoute(), dispatcher, logger)

		req := httptest.NewRequest(http.MethodPost, "/orders", strings.NewReader(`{"orderId":"o1"}`))
		rec := httptest.NewRecorder()
		handler(rec, req)

		if rec.Code != http.StatusAccepted {
			t.Fatalf("status = %d, want %d (body: %s)", rec.Code, http.StatusAccepted, rec.Body.String())
		}
		for _, id := range []string{"order-o1", "order-o1-notification"} {
			if !dispatcher.wasDispatched(id) {
				t.Errorf("expected workflow %q to be dispatched", id)
			}
		}
	})

	// Regression test: the dispatch loop used to `return` as soon as the
	// first binding failed, so a failure on binding 0 silently skipped
	// binding 1 - "only one of the two configured workflows starts". Each
	// binding must be attempted independently of the others' outcome.
	t.Run("the first binding failing does not prevent the second from being attempted", func(t *testing.T) {
		dispatcher := &trackingDispatcher{failIDs: map[string]bool{"order-o1": true}}
		handler := dispatchHandler(twoBindingRoute(), dispatcher, logger)

		req := httptest.NewRequest(http.MethodPost, "/orders", strings.NewReader(`{"orderId":"o1"}`))
		rec := httptest.NewRecorder()
		handler(rec, req)

		if rec.Code == http.StatusAccepted {
			t.Fatalf("expected a failure status since order-o1 was made to fail, got %d", rec.Code)
		}
		if !dispatcher.wasDispatched("order-o1-notification") {
			t.Error("expected the second binding to still be dispatched even though the first failed")
		}
	})

	// Same as above, with the failure on the later binding instead, to rule
	// out any accidental dependency on slice order.
	t.Run("the second binding failing does not prevent the first from being attempted", func(t *testing.T) {
		dispatcher := &trackingDispatcher{failIDs: map[string]bool{"order-o1-notification": true}}
		handler := dispatchHandler(twoBindingRoute(), dispatcher, logger)

		req := httptest.NewRequest(http.MethodPost, "/orders", strings.NewReader(`{"orderId":"o1"}`))
		rec := httptest.NewRecorder()
		handler(rec, req)

		if rec.Code == http.StatusAccepted {
			t.Fatalf("expected a failure status since order-o1-notification was made to fail, got %d", rec.Code)
		}
		if !dispatcher.wasDispatched("order-o1") {
			t.Error("expected the first binding to still be dispatched even though the second failed")
		}
	})
}

func createOrderRoute() spec.Route {
	return spec.Route{
		Method: "POST",
		Path:   "/orders",
		Operation: &spec.Operation{
			OperationID: "createOrder",
			RequestBody: &spec.RequestBody{
				Required: true,
				Content: map[string]spec.MediaType{
					"application/json": {
						Schema: map[string]any{
							"type":     "object",
							"required": []any{"orderId", "customerId"},
							"properties": map[string]any{
								"orderId":    map[string]any{"type": "string"},
								"customerId": map[string]any{"type": "string"},
							},
						},
					},
				},
			},
			Temporal: []spec.TemporalBinding{
				{Action: spec.ActionStartWorkflow, WorkflowType: "OrderWorkflow", WorkflowID: "order-{body.orderId}"},
			},
		},
	}
}

func TestDispatchHandlerValidatesBody(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	tests := []struct {
		name       string
		body       string
		wantStatus int
		wantCalled bool
	}{
		{"missing required field", `{"customerId":"c1"}`, http.StatusBadRequest, false},
		{"wrong field type", `{"orderId":123,"customerId":"c1"}`, http.StatusBadRequest, false},
		{"empty body when required", ``, http.StatusBadRequest, false},
		{"malformed json", `{`, http.StatusBadRequest, false},
		{"valid payload", `{"orderId":"o1","customerId":"c1"}`, http.StatusAccepted, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dispatcher := &recordingDispatcher{}
			handler := dispatchHandler(createOrderRoute(), dispatcher, logger)

			req := httptest.NewRequest(http.MethodPost, "/orders", strings.NewReader(tt.body))
			rec := httptest.NewRecorder()

			handler(rec, req)

			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d (body: %s)", rec.Code, tt.wantStatus, rec.Body.String())
			}
			if dispatcher.called != tt.wantCalled {
				t.Errorf("dispatcher called = %v, want %v", dispatcher.called, tt.wantCalled)
			}
		})
	}
}

func TestDispatchHandlerValidationResponseShape(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	dispatcher := &recordingDispatcher{}
	handler := dispatchHandler(createOrderRoute(), dispatcher, logger)

	// Two independent problems: orderId missing, customerId wrong type.
	// Both should show up in one response instead of just the first found.
	req := httptest.NewRequest(http.MethodPost, "/orders", strings.NewReader(`{"customerId":123}`))
	rec := httptest.NewRecorder()

	handler(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d (body: %s)", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	if dispatcher.called {
		t.Error("dispatcher should not be called for an invalid request")
	}

	var result validate.Result
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("response body is not valid JSON: %v (body: %s)", err, rec.Body.String())
	}

	if result.Success {
		t.Error("Success = true, want false")
	}
	if result.Message == "" {
		t.Error("Message is empty, want a human-readable summary")
	}
	if _, ok := result.Fields["orderId"]; !ok {
		t.Errorf("Fields missing \"orderId\": %+v", result.Fields)
	}
	if _, ok := result.Fields["customerId"]; !ok {
		t.Errorf("Fields missing \"customerId\": %+v", result.Fields)
	}
}
