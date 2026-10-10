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
	"time"

	"go.temporal.io/api/serviceerror"

	"temporal-gateway/internal/response"
	"temporal-gateway/internal/spec"
)

// trackingDispatcher records, under a mutex, every workflowID it was asked
// to dispatch. It fails dispatch (a Go error) for any workflowID listed in
// failIDs, and simulates "attached to an already-existing run" (dispatch
// succeeds with no error, but Status.IsError() is true) for any workflowID
// listed in existingIDs. Used to prove that one binding's outcome doesn't
// affect whether another is attempted, and that a non-error, non-STARTED
// result is treated as not having started.
type trackingDispatcher struct {
	failIDs     map[string]bool
	existingIDs map[string]bool

	mu   sync.Mutex
	seen map[string]bool
}

func (d *trackingDispatcher) Dispatch(_ context.Context, _ spec.TemporalBinding, workflowID string, _ any) (any, error) {
	d.mu.Lock()
	if d.seen == nil {
		d.seen = map[string]bool{}
	}
	d.seen[workflowID] = true
	d.mu.Unlock()

	if d.failIDs[workflowID] {
		return nil, fmt.Errorf("dispatch failed for %s", workflowID)
	}
	if d.existingIDs[workflowID] {
		return response.WorkflowStarted{
			Envelope:   response.Envelope{Status: response.StatusWorkflowRunning, Message: "a workflow with this ID is already running"},
			WorkflowID: workflowID,
		}, nil
	}
	return response.WorkflowStarted{
		Envelope:   response.Envelope{Status: response.StatusStarted},
		WorkflowID: workflowID,
	}, nil
}

func (d *trackingDispatcher) wasDispatched(workflowID string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.seen[workflowID]
}

// failingDispatcher always returns err, and is used to check that a
// single-binding route still surfaces the specific HTTP status for that
// error (e.g. 404) rather than 207 - Multi-Status is only for an actual
// batch of more than one binding.
type failingDispatcher struct {
	err error
}

func (d *failingDispatcher) Dispatch(_ context.Context, _ spec.TemporalBinding, _ string, _ any) (any, error) {
	return nil, d.err
}

func twoBindingRoute() spec.Route {
	return spec.Route{
		Method: "POST",
		Path:   "/orders",
		Operation: &spec.Operation{
			OperationID: "createOrder",
			Temporal: spec.TemporalSpec{
				Triggers: []spec.TemporalBinding{
					{Action: spec.ActionStartWorkflow, WorkflowType: "OrderWorkflow", WorkflowID: "order-{body.orderId}"},
					{Action: spec.ActionStartWorkflow, WorkflowType: "NotificationWorkflow", WorkflowID: "order-{body.orderId}-notification"},
				},
			},
		},
	}
}

// twoBindingRouteSingle is twoBindingRoute() trimmed to just its first
// binding, so the single-binding status-code path can be exercised with
// the same workflow naming as the multi-binding tests.
func twoBindingRouteSingle() spec.Route {
	route := twoBindingRoute()
	route.Operation.Temporal.Triggers = route.Operation.Temporal.Triggers[:1]
	return route
}

// allOrNothingRoute is twoBindingRoute() with returnStrategy set to
// allOrNothing, so the "reject on any incomplete trigger" path can be
// exercised with the same workflow naming as the acceptPartial tests.
func allOrNothingRoute() spec.Route {
	route := twoBindingRoute()
	route.Operation.Temporal.ReturnStrategy = spec.ReturnStrategyAllOrNothing
	return route
}

func TestDispatchHandlerRunsAllBindingsEvenWhenOneFails(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	t.Run("both bindings dispatch when all succeed", func(t *testing.T) {
		dispatcher := &trackingDispatcher{}
		handler := dispatchHandler(twoBindingRoute(), dispatcher, Options{}, logger)

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

		var batch struct {
			Status  string           `json:"status"`
			Results []map[string]any `json:"results"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &batch); err != nil {
			t.Fatalf("response body is not the expected JSON object: %v (body: %s)", err, rec.Body.String())
		}
		if batch.Status != string(response.StatusWorkflowStarted) {
			t.Errorf("top-level status = %q, want %q", batch.Status, response.StatusWorkflowStarted)
		}
		if len(batch.Results) != 2 {
			t.Errorf("expected 2 results, got %d: %+v", len(batch.Results), batch.Results)
		}
	})

	// Regression test: the dispatch loop used to `return` as soon as the
	// first binding failed, so a failure on binding 0 silently skipped
	// binding 1 - "only one of the two configured workflows starts". Each
	// binding must be attempted independently of the others' outcome, and
	// the mixed outcome must come back as 207 Multi-Status with a per-item
	// breakdown so the caller can tell exactly which one failed and why.
	t.Run("the first binding failing does not prevent the second from being attempted", func(t *testing.T) {
		dispatcher := &trackingDispatcher{failIDs: map[string]bool{"order-o1": true}}
		handler := dispatchHandler(twoBindingRoute(), dispatcher, Options{}, logger)

		req := httptest.NewRequest(http.MethodPost, "/orders", strings.NewReader(`{"orderId":"o1"}`))
		rec := httptest.NewRecorder()
		handler(rec, req)

		if rec.Code != http.StatusMultiStatus {
			t.Fatalf("status = %d, want %d (body: %s)", rec.Code, http.StatusMultiStatus, rec.Body.String())
		}
		if !dispatcher.wasDispatched("order-o1-notification") {
			t.Error("expected the second binding to still be dispatched even though the first failed")
		}

		var batch struct {
			Status  string           `json:"status"`
			Message string           `json:"message"`
			Results []map[string]any `json:"results"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &batch); err != nil {
			t.Fatalf("response body is not the expected JSON object: %v (body: %s)", err, rec.Body.String())
		}
		if batch.Status != string(response.StatusWorkflowPartiallyStarted) {
			t.Errorf("top-level status = %q, want %q", batch.Status, response.StatusWorkflowPartiallyStarted)
		}
		items := batch.Results
		if len(items) != 2 {
			t.Fatalf("expected 2 items, got %d: %+v", len(items), items)
		}
		if items[0]["status"] != string(response.StatusFailed) {
			t.Errorf("items[0].status = %v, want %q", items[0]["status"], response.StatusFailed)
		}
		msg, _ := items[0]["message"].(string)
		if !strings.Contains(msg, "OrderWorkflow") || !strings.Contains(msg, "order-o1") {
			t.Errorf("items[0].message = %q, want it to name the failing workflow (OrderWorkflow / order-o1)", msg)
		}
		if items[1]["status"] != string(response.StatusStarted) {
			t.Errorf("items[1].status = %v, want %q", items[1]["status"], response.StatusStarted)
		}
	})

	// Same as above, with the failure on the later binding instead, to rule
	// out any accidental dependency on slice order.
	t.Run("the second binding failing does not prevent the first from being attempted", func(t *testing.T) {
		dispatcher := &trackingDispatcher{failIDs: map[string]bool{"order-o1-notification": true}}
		handler := dispatchHandler(twoBindingRoute(), dispatcher, Options{}, logger)

		req := httptest.NewRequest(http.MethodPost, "/orders", strings.NewReader(`{"orderId":"o1"}`))
		rec := httptest.NewRecorder()
		handler(rec, req)

		if rec.Code != http.StatusMultiStatus {
			t.Fatalf("status = %d, want %d (body: %s)", rec.Code, http.StatusMultiStatus, rec.Body.String())
		}
		if !dispatcher.wasDispatched("order-o1") {
			t.Error("expected the first binding to still be dispatched even though the second failed")
		}

		var batch struct {
			Status  string           `json:"status"`
			Message string           `json:"message"`
			Results []map[string]any `json:"results"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &batch); err != nil {
			t.Fatalf("response body is not the expected JSON object: %v (body: %s)", err, rec.Body.String())
		}
		if batch.Status != string(response.StatusWorkflowPartiallyStarted) {
			t.Errorf("top-level status = %q, want %q", batch.Status, response.StatusWorkflowPartiallyStarted)
		}
		items := batch.Results
		if len(items) != 2 {
			t.Fatalf("expected 2 items, got %d: %+v", len(items), items)
		}
		if items[0]["status"] != string(response.StatusStarted) {
			t.Errorf("items[0].status = %v, want %q", items[0]["status"], response.StatusStarted)
		}
		if items[1]["status"] != string(response.StatusFailed) {
			t.Errorf("items[1].status = %v, want %q", items[1]["status"], response.StatusFailed)
		}
		msg, _ := items[1]["message"].(string)
		if !strings.Contains(msg, "NotificationWorkflow") || !strings.Contains(msg, "order-o1-notification") {
			t.Errorf("items[1].message = %q, want it to name the failing workflow (NotificationWorkflow / order-o1-notification)", msg)
		}
	})
}

// Regression test for the "multistatus when at least one workflow started"
// correction: 207 is reserved for a genuine mix of outcomes. If nothing in
// the batch started, that's a uniform failure (however many different
// underlying reasons) and should get an ordinary single status code, not
// 207.
func TestDispatchHandlerNoBindingsStartedIsNotMultiStatus(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	dispatcher := &trackingDispatcher{failIDs: map[string]bool{"order-o1": true, "order-o1-notification": true}}
	handler := dispatchHandler(twoBindingRoute(), dispatcher, Options{}, logger)

	req := httptest.NewRequest(http.MethodPost, "/orders", strings.NewReader(`{"orderId":"o1"}`))
	rec := httptest.NewRecorder()
	handler(rec, req)

	if rec.Code == http.StatusMultiStatus {
		t.Fatalf("status = %d, want anything but %d since nothing started", rec.Code, http.StatusMultiStatus)
	}

	var batch struct {
		Status  string           `json:"status"`
		Results []map[string]any `json:"results"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &batch); err != nil {
		t.Fatalf("response body is not the expected JSON object: %v (body: %s)", err, rec.Body.String())
	}
	if batch.Status != string(response.StatusWorkflowNotStarted) {
		t.Errorf("top-level status = %q, want %q", batch.Status, response.StatusWorkflowNotStarted)
	}
	if len(batch.Results) != 2 {
		t.Fatalf("expected 2 items, got %d: %+v", len(batch.Results), batch.Results)
	}
}

// Regression test for the core bug report: a startWorkflow binding can
// return successfully (no Go error) while attaching to an already-existing
// run instead of creating a new one - that must not be reported as STARTED.
func TestDispatchHandlerAttachingToExistingRunIsNotStarted(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	t.Run("single binding attaching to an existing run returns 409, not STARTED", func(t *testing.T) {
		route := twoBindingRoute()
		route.Operation.Temporal.Triggers = route.Operation.Temporal.Triggers[:1]
		dispatcher := &trackingDispatcher{existingIDs: map[string]bool{"order-o1": true}}
		handler := dispatchHandler(route, dispatcher, Options{}, logger)

		req := httptest.NewRequest(http.MethodPost, "/orders", strings.NewReader(`{"orderId":"o1"}`))
		rec := httptest.NewRecorder()
		handler(rec, req)

		if rec.Code != http.StatusConflict {
			t.Fatalf("status = %d, want %d (body: %s)", rec.Code, http.StatusConflict, rec.Body.String())
		}

		var item map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &item); err != nil {
			t.Fatalf("response body is not a JSON object: %v (body: %s)", err, rec.Body.String())
		}
		if item["status"] == string(response.StatusStarted) {
			t.Error("status = STARTED, want the actual pre-existing workflow state instead")
		}
		if item["status"] != string(response.StatusWorkflowRunning) {
			t.Errorf("status = %v, want %q", item["status"], response.StatusWorkflowRunning)
		}
	})

	t.Run("one binding attaching to an existing run while another genuinely starts is a multi-status mix", func(t *testing.T) {
		dispatcher := &trackingDispatcher{existingIDs: map[string]bool{"order-o1": true}}
		handler := dispatchHandler(twoBindingRoute(), dispatcher, Options{}, logger)

		req := httptest.NewRequest(http.MethodPost, "/orders", strings.NewReader(`{"orderId":"o1"}`))
		rec := httptest.NewRecorder()
		handler(rec, req)

		if rec.Code != http.StatusMultiStatus {
			t.Fatalf("status = %d, want %d (body: %s)", rec.Code, http.StatusMultiStatus, rec.Body.String())
		}

		var batch struct {
			Status  string           `json:"status"`
			Results []map[string]any `json:"results"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &batch); err != nil {
			t.Fatalf("response body is not the expected JSON object: %v (body: %s)", err, rec.Body.String())
		}
		if batch.Status != string(response.StatusWorkflowPartiallyStarted) {
			t.Errorf("top-level status = %q, want %q", batch.Status, response.StatusWorkflowPartiallyStarted)
		}
		if len(batch.Results) != 2 {
			t.Fatalf("expected 2 items, got %d: %+v", len(batch.Results), batch.Results)
		}
		if batch.Results[0]["status"] != string(response.StatusWorkflowRunning) {
			t.Errorf("items[0].status = %v, want %q", batch.Results[0]["status"], response.StatusWorkflowRunning)
		}
		if batch.Results[1]["status"] != string(response.StatusStarted) {
			t.Errorf("items[1].status = %v, want %q", batch.Results[1]["status"], response.StatusStarted)
		}
	})
}

func TestDispatchHandlerSingleBindingFailureKeepsSpecificStatus(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	dispatcher := &failingDispatcher{err: serviceerror.NewNotFound("workflow not found")}
	handler := dispatchHandler(twoBindingRouteSingle(), dispatcher, Options{}, logger)

	req := httptest.NewRequest(http.MethodPost, "/orders", strings.NewReader(`{"orderId":"o1"}`))
	rec := httptest.NewRecorder()
	handler(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d (body: %s)", rec.Code, http.StatusNotFound, rec.Body.String())
	}

	var envelope map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("response body is not a JSON object: %v (body: %s)", err, rec.Body.String())
	}
	msg, _ := envelope["message"].(string)
	if !strings.Contains(msg, "OrderWorkflow") {
		t.Errorf("message = %q, want it to name the failing workflow (OrderWorkflow)", msg)
	}
}

// TestDispatchHandlerAllOrNothingRejectsAnyIncompleteTrigger checks
// x-temporal.returnStrategy: allOrNothing - a genuine mix of outcomes that
// would be 207 Multi-Status under the default acceptPartial strategy
// instead comes back as a single 409 Conflict, since not every trigger
// completed.
func TestDispatchHandlerAllOrNothingRejectsAnyIncompleteTrigger(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	dispatcher := &trackingDispatcher{failIDs: map[string]bool{"order-o1": true}}
	handler := dispatchHandler(allOrNothingRoute(), dispatcher, Options{}, logger)

	req := httptest.NewRequest(http.MethodPost, "/orders", strings.NewReader(`{"orderId":"o1"}`))
	rec := httptest.NewRecorder()
	handler(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d (body: %s)", rec.Code, http.StatusConflict, rec.Body.String())
	}
	if !dispatcher.wasDispatched("order-o1-notification") {
		t.Error("expected the second binding to still be attempted even though the first failed and the overall result was rejected")
	}

	var batch struct {
		Status  string           `json:"status"`
		Results []map[string]any `json:"results"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &batch); err != nil {
		t.Fatalf("response body is not the expected JSON object: %v (body: %s)", err, rec.Body.String())
	}
	if batch.Status != string(response.StatusWorkflowNotStarted) {
		t.Errorf("top-level status = %q, want %q", batch.Status, response.StatusWorkflowNotStarted)
	}
	if len(batch.Results) != 2 {
		t.Fatalf("expected 2 items, got %d: %+v", len(batch.Results), batch.Results)
	}
}

// TestDispatchHandlerAllOrNothingAcceptsUniformSuccess checks that
// allOrNothing doesn't change anything about the case where every trigger
// actually does succeed.
func TestDispatchHandlerAllOrNothingAcceptsUniformSuccess(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	dispatcher := &trackingDispatcher{}
	handler := dispatchHandler(allOrNothingRoute(), dispatcher, Options{}, logger)

	req := httptest.NewRequest(http.MethodPost, "/orders", strings.NewReader(`{"orderId":"o1"}`))
	rec := httptest.NewRecorder()
	handler(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d (body: %s)", rec.Code, http.StatusAccepted, rec.Body.String())
	}

	var batch struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &batch); err != nil {
		t.Fatalf("response body is not the expected JSON object: %v (body: %s)", err, rec.Body.String())
	}
	if batch.Status != string(response.StatusWorkflowStarted) {
		t.Errorf("top-level status = %q, want %q", batch.Status, response.StatusWorkflowStarted)
	}
}

// TestDispatchHandlerAllOrNothingRejectsUniformFailure checks that a total
// failure under allOrNothing also comes back as 409, not whatever
// error-specific status the first item would carry under acceptPartial.
func TestDispatchHandlerAllOrNothingRejectsUniformFailure(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	dispatcher := &trackingDispatcher{failIDs: map[string]bool{"order-o1": true, "order-o1-notification": true}}
	handler := dispatchHandler(allOrNothingRoute(), dispatcher, Options{}, logger)

	req := httptest.NewRequest(http.MethodPost, "/orders", strings.NewReader(`{"orderId":"o1"}`))
	rec := httptest.NewRecorder()
	handler(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d (body: %s)", rec.Code, http.StatusConflict, rec.Body.String())
	}
}

// deadlineDispatcher blocks until ctx is done and returns its error, like a
// getResult on a workflow that outlives the request timeout.
type deadlineDispatcher struct{}

func (deadlineDispatcher) Dispatch(ctx context.Context, _ spec.TemporalBinding, _ string, _ any) (any, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func decodeEnvelope(t *testing.T, rec *httptest.ResponseRecorder) response.Envelope {
	t.Helper()
	var envelope response.Envelope
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("response body is not a JSON envelope: %v (body: %s)", err, rec.Body.String())
	}
	return envelope
}

func TestDispatchHandlerRejectsOversizedBody(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	dispatcher := &trackingDispatcher{}
	handler := dispatchHandler(twoBindingRouteSingle(), dispatcher, Options{MaxBodyBytes: 16}, logger)

	req := httptest.NewRequest(http.MethodPost, "/orders", strings.NewReader(`{"orderId":"`+strings.Repeat("x", 64)+`"}`))
	rec := httptest.NewRecorder()
	handler(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413 (body: %s)", rec.Code, rec.Body.String())
	}
	if got := decodeEnvelope(t, rec).Status; got != response.StatusPayloadTooLarge {
		t.Errorf("status = %q, want %q", got, response.StatusPayloadTooLarge)
	}
	if len(dispatcher.seen) != 0 {
		t.Errorf("dispatched %v, want nothing dispatched", dispatcher.seen)
	}
}

func TestDispatchHandlerRejectsUnresolvedPlaceholderWithoutDispatching(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	dispatcher := &trackingDispatcher{}
	handler := dispatchHandler(twoBindingRoute(), dispatcher, Options{}, logger)

	req := httptest.NewRequest(http.MethodPost, "/orders", strings.NewReader(`{"customerId":"c1"}`))
	rec := httptest.NewRecorder()
	handler(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 (body: %s)", rec.Code, rec.Body.String())
	}
	envelope := decodeEnvelope(t, rec)
	if envelope.Status != response.StatusInvalidRequest || !strings.Contains(envelope.Message, "{body.orderId}") {
		t.Errorf("envelope = %+v, want INVALID_REQUEST naming {body.orderId}", envelope)
	}
	if len(dispatcher.seen) != 0 {
		t.Errorf("dispatched %v, want nothing dispatched", dispatcher.seen)
	}
}

func TestDispatchHandlerTimesOutSlowDispatch(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := dispatchHandler(twoBindingRouteSingle(), deadlineDispatcher{}, Options{RequestTimeout: 10 * time.Millisecond}, logger)

	req := httptest.NewRequest(http.MethodPost, "/orders", strings.NewReader(`{"orderId":"o1"}`))
	rec := httptest.NewRecorder()
	handler(rec, req)

	if rec.Code != http.StatusGatewayTimeout {
		t.Fatalf("status = %d, want 504 (body: %s)", rec.Code, rec.Body.String())
	}
	if got := decodeEnvelope(t, rec).Status; got != response.StatusTimeout {
		t.Errorf("status = %q, want %q", got, response.StatusTimeout)
	}
}

func TestDispatchHandlerDoesNotLeakRawTemporalErrors(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	dispatcher := &failingDispatcher{err: serviceerror.NewUnavailable("dial tcp 10.0.0.7:7233: namespace payments-prod unreachable")}
	handler := dispatchHandler(twoBindingRouteSingle(), dispatcher, Options{}, logger)

	req := httptest.NewRequest(http.MethodPost, "/orders", strings.NewReader(`{"orderId":"o1"}`))
	rec := httptest.NewRecorder()
	handler(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502 (body: %s)", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); strings.Contains(body, "10.0.0.7") || strings.Contains(body, "payments-prod") {
		t.Errorf("response leaks the raw Temporal error: %s", body)
	}
}
