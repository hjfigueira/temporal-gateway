package gateway

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"temporal-gateway/internal/response"
	"temporal-gateway/internal/spec"
	"temporal-gateway/internal/validate"
)

// recordingDispatcher implements Dispatcher and records whether it was
// invoked, so tests can assert that an invalid request never reaches it.
type recordingDispatcher struct {
	called bool
}

func (d *recordingDispatcher) Dispatch(ctx context.Context, binding spec.TemporalBinding, workflowID string, body any) (any, error) {
	d.called = true
	return map[string]string{"workflowId": workflowID}, nil
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

	if !result.Status.IsError() {
		t.Error("expected an error status")
	}
	if result.Status != response.StatusValidationFailed {
		t.Errorf("Status = %q, want %q", result.Status, response.StatusValidationFailed)
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
