package gateway

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.temporal.io/api/serviceerror"

	"temporal-gateway/internal/response"
	"temporal-gateway/internal/spec"
)

func discard() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// TestNewHandlerRoutesPathParamsAndLabelsActionlessFailures drives a real
// mux built by NewHandler: the path parameter feeds the workflowId, and a
// failing binding with no workflowType is labelled by its action.
func TestNewHandlerRoutesPathParamsAndLabelsActionlessFailures(t *testing.T) {
	op := &spec.Operation{
		OperationID: "cancelOrder",
		Temporal: spec.TemporalSpec{Triggers: []spec.TemporalBinding{
			{Action: spec.ActionSignalWorkflow, SignalName: "cancel", WorkflowID: "order-{path.orderId}"},
		}},
	}
	apiSpec := &spec.Spec{Paths: map[string]spec.PathItem{"/orders/{orderId}/cancel": {Post: op}}}
	handler := NewHandler(apiSpec, &failingDispatcher{err: errors.New("boom")}, Options{}, discard())

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/orders/o7/cancel", nil))

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502 (body: %s)", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); !strings.Contains(body, "signalWorkflow (order-o7)") {
		t.Fatalf("body = %s, want the failure labelled %q", body, "signalWorkflow (order-o7)")
	}
}

// TestDispatchHandlerTracesAndDefaultsUnmappedActionTo200 runs with a real
// TracerProvider (so the request logger gets trace/span IDs) and a binding
// whose action has no statusByAction entry.
func TestDispatchHandlerTracesAndDefaultsUnmappedActionTo200(t *testing.T) {
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(sdktrace.NewTracerProvider())
	t.Cleanup(func() { otel.SetTracerProvider(prev) })

	route := twoBindingRouteSingle()
	route.Operation.Temporal.Triggers[0].Action = "customAction"
	handler := dispatchHandler(route, &trackingDispatcher{}, Options{}, discard())

	rec := httptest.NewRecorder()
	handler(rec, httptest.NewRequest(http.MethodPost, "/orders", strings.NewReader(`{"orderId":"o1"}`)))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestErrorResponse(t *testing.T) {
	tests := []struct {
		err        error
		wantStatus response.Status
		wantHTTP   int
	}{
		{serviceerror.NewNotFound("x"), response.StatusNotFound, http.StatusNotFound},
		{serviceerror.NewWorkflowExecutionAlreadyStarted("x", "", ""), response.StatusDuplicated, http.StatusConflict},
		{serviceerror.NewInvalidArgument("x"), response.StatusInvalidArgument, http.StatusBadRequest},
		{serviceerror.NewPermissionDenied("x", ""), response.StatusForbidden, http.StatusForbidden},
		{serviceerror.NewDeadlineExceeded("x"), response.StatusTimeout, http.StatusGatewayTimeout},
		{serviceerror.NewUnavailable("x"), response.StatusUnavailable, http.StatusServiceUnavailable},
		{context.DeadlineExceeded, response.StatusTimeout, http.StatusGatewayTimeout},
		{errors.New("x"), response.StatusFailed, http.StatusBadGateway},
	}
	for _, tt := range tests {
		status, code, msg := errorResponse(tt.err)
		if status != tt.wantStatus || code != tt.wantHTTP || msg == "" || strings.Contains(msg, "x") {
			t.Errorf("errorResponse(%T) = %q, %d, %q; want %q, %d, a fixed message", tt.err, status, code, msg, tt.wantStatus, tt.wantHTTP)
		}
	}
}

func TestValidateBodyWithoutSchema(t *testing.T) {
	if r := validateBody(nil, map[string]any{}); r.Status.IsError() {
		t.Errorf("nil requestBody: %+v", r)
	}
	if r := validateBody(&spec.RequestBody{}, nil); r.Status.IsError() {
		t.Errorf("optional body, none sent: %+v", r)
	}
	rb := &spec.RequestBody{Content: map[string]spec.MediaType{"text/plain": {}}}
	if r := validateBody(rb, map[string]any{"a": 1.0}); r.Status.IsError() {
		t.Errorf("no application/json schema: %+v", r)
	}
}

func TestFieldResolverMissingValues(t *testing.T) {
	resolve := fieldResolver(httptest.NewRequest(http.MethodGet, "/", nil), nil, map[string]any{"empty": nil})
	for _, name := range []string{"query.absent", "header.Absent"} {
		if _, ok := resolve(name); ok {
			t.Errorf("resolve(%q) ok = true, want false", name)
		}
	}
	if v, ok := resolve("body.empty"); !ok || v != "" {
		t.Errorf(`resolve("body.empty") = %q, %v; want "", true`, v, ok)
	}
}
