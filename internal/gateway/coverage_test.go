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

// rawDispatcher answers every dispatch with a raw result, like a query.
type rawDispatcher struct{}

func (rawDispatcher) Dispatch(context.Context, spec.TemporalBinding, string, any) (response.Outcome, error) {
	return response.Raw(map[string]string{"total": "42"}), nil
}

// TestDispatchHandlerTracesAndAnswersRawResultsWith200 runs with a real
// TracerProvider (so the request logger gets trace/span IDs) and a
// dispatcher returning a raw result, which gets 200 rather than 202.
func TestDispatchHandlerTracesAndAnswersRawResultsWith200(t *testing.T) {
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(sdktrace.NewTracerProvider())
	t.Cleanup(func() { otel.SetTracerProvider(prev) })

	route := twoBindingRouteSingle()
	handler := dispatchHandler(route, rawDispatcher{}, Options{}, discard())

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
