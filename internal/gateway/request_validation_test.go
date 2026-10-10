package gateway

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"temporal-gateway/internal/response"
	"temporal-gateway/internal/spec"
)

// recordingDispatcher implements Dispatcher and records whether it was
// invoked, so tests can assert that an invalid request never reaches it.
type recordingDispatcher struct {
	called bool
}

func (d *recordingDispatcher) Dispatch(_ context.Context, _ spec.TemporalBinding, workflowID string, _ any) (any, error) {
	d.called = true
	return map[string]string{"workflowId": workflowID}, nil
}

const ordersSpec = `openapi: 3.0.3
info: {title: Orders, version: "1"}
security:
  - apiKey: []
components:
  securitySchemes:
    apiKey: {type: apiKey, in: header, name: X-Api-Key}
paths:
  /orders/{region}:
    post:
      operationId: createOrder
      parameters:
        - {name: region, in: path, required: true, schema: {type: string, enum: [eu, us]}}
        - {name: limit, in: query, schema: {type: integer, maximum: 10}}
      requestBody:
        required: true
        content:
          application/json:
            schema:
              type: object
              additionalProperties: false
              required: [orderId, customerId]
              properties:
                orderId: {type: string}
                customerId: {type: string}
                name: {type: string, minLength: 4, maxLength: 4}
                quantity: {type: integer}
                items:
                  type: array
                  items:
                    type: object
                    properties:
                      sku: {type: string, pattern: "^[A-Z]+$"}
      responses:
        "202": {description: Started.}
      x-temporal:
        triggers:
          - action: startWorkflow
            namespace: default
            workflowType: OrderWorkflow
            taskQueue: orders
            workflowId: "order-{body.orderId}"
`

// ordersMux serves route the way NewHandler does, so path values resolve.
func ordersMux(route spec.Route, dispatcher Dispatcher, logger *slog.Logger) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc(route.Method+" "+route.Path, dispatchHandler(route, dispatcher, Options{}, logger))
	return mux
}

// ordersRoute loads ordersSpec through spec.Load, as the gateway does.
func ordersRoute(t *testing.T) spec.Route {
	t.Helper()
	path := filepath.Join(t.TempDir(), "api-spec.yaml")
	if err := os.WriteFile(path, []byte(ordersSpec), 0o644); err != nil {
		t.Fatal(err)
	}
	apiSpec, err := spec.Load(path)
	if err != nil {
		t.Fatalf("load spec: %v", err)
	}
	return apiSpec.Routes()[0]
}

func TestDispatchHandlerValidatesRequest(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	route := ordersRoute(t)

	tests := []struct {
		name        string
		target      string
		contentType string
		body        string
		wantStatus  int
	}{
		{"valid payload", "/orders/eu", "", `{"orderId":"o1","customerId":"c1"}`, http.StatusAccepted},
		{"valid with explicit content type", "/orders/eu?limit=3", "application/json", `{"orderId":"o1","customerId":"c1"}`, http.StatusAccepted},
		{"length counts characters", "/orders/eu", "", `{"orderId":"o1","customerId":"c1","name":"José"}`, http.StatusAccepted},
		{"integer beyond float64 precision", "/orders/eu", "", `{"orderId":"o1","customerId":"c1","quantity":12345678901234567}`, http.StatusAccepted},
		{"missing required field", "/orders/eu", "", `{"customerId":"c1"}`, http.StatusUnprocessableEntity},
		{"wrong field type", "/orders/eu", "", `{"orderId":123,"customerId":"c1"}`, http.StatusUnprocessableEntity},
		{"empty body when required", "/orders/eu", "", ``, http.StatusUnprocessableEntity},
		{"malformed json", "/orders/eu", "", `{`, http.StatusUnprocessableEntity},
		{"path param outside enum", "/orders/mars", "", `{"orderId":"o1","customerId":"c1"}`, http.StatusUnprocessableEntity},
		{"query param above maximum", "/orders/eu?limit=20", "", `{"orderId":"o1","customerId":"c1"}`, http.StatusUnprocessableEntity},
		{"undeclared content type", "/orders/eu", "text/plain", `{"orderId":"o1","customerId":"c1"}`, http.StatusUnprocessableEntity},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dispatcher := &recordingDispatcher{}
			req := httptest.NewRequest(http.MethodPost, tt.target, strings.NewReader(tt.body))
			if tt.contentType != "" {
				req.Header.Set("Content-Type", tt.contentType)
			}
			rec := httptest.NewRecorder()
			ordersMux(route, dispatcher, logger).ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d (body: %s)", rec.Code, tt.wantStatus, rec.Body.String())
			}
			if wantCalled := tt.wantStatus == http.StatusAccepted; dispatcher.called != wantCalled {
				t.Errorf("dispatcher called = %v, want %v", dispatcher.called, wantCalled)
			}
		})
	}
}

func TestDispatchHandlerValidationResponseShape(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mux := ordersMux(ordersRoute(t), &recordingDispatcher{}, logger)

	// Every independent problem shows up in one response, keyed by field.
	// kin-openapi reports an undeclared property on its parent object, here
	// the body itself.
	body := `{"customerId":123,"secret":true,"items":[{"sku":"OK"},{"sku":"bad"}]}`
	req := httptest.NewRequest(http.MethodPost, "/orders/mars?limit=20", strings.NewReader(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 (body: %s)", rec.Code, rec.Body.String())
	}
	var result response.ValidationFailed
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("response body is not valid JSON: %v (body: %s)", err, rec.Body.String())
	}
	if result.Status != response.StatusValidationFailed || result.Message == "" {
		t.Errorf("envelope = %+v, want VALIDATION_FAILED with a summary", result.Envelope)
	}
	got := slices.Sorted(maps.Keys(result.Fields))
	want := []string{"_body", "customerId", "items[1].sku", "orderId", "path.region", "query.limit"}
	if !slices.Equal(got, want) {
		t.Errorf("fields = %v, want %v (body: %s)", result.Fields, want, rec.Body.String())
	}
}
