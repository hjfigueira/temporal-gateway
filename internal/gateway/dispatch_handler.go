package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"

	"go.temporal.io/api/serviceerror"

	"temporal-gateway/internal/response"
	"temporal-gateway/internal/spec"
)

// dispatchOutcome is one binding's result from a parallel dispatch: either
// result or err is set, never both.
type dispatchOutcome struct {
	workflowID string
	result     any
	err        error
}

// statusGetter is satisfied by every response type in internal/response
// (via Envelope's promoted GetStatus method), letting dispatchHandler read
// a dispatch outcome's Status without a type switch over every concrete
// response type.
type statusGetter interface {
	GetStatus() response.Status
}

// succeeded reports whether a dispatch outcome actually did what was
// asked. A nil Go error alone isn't enough: e.g. a startWorkflow binding
// can return successfully while attaching to an already-existing run
// instead of creating a new one (see internal/temporal.classifyExistingRun),
// which is a Status.IsError() outcome despite there being no error.
func (o dispatchOutcome) succeeded() bool {
	if o.err != nil {
		return false
	}
	if sg, ok := o.result.(statusGetter); ok {
		return !sg.GetStatus().IsError()
	}
	return true
}

// dispatchHandler resolves path params and the JSON body, then renders and
// dispatches each of the operation's x-temporal bindings in parallel, so
// one HTTP call can trigger several Temporal actions at once and a failure
// in one binding doesn't prevent the others from starting. When more than
// one binding is dispatched, the response is a response.BatchResult: a
// top-level status of WORKFLOW_STARTED if every binding succeeded,
// WORKFLOW_NOT_STARTED if none did, or WORKFLOW_PARTIALLY_STARTED if it's a
// genuine mix of both - plus each binding's own per-item result (each
// naming which workflow it's about). Only that genuine-mix case is reported
// under HTTP 207 Multi-Status; a uniform outcome (all started, or none did)
// gets an ordinary single status code.
func dispatchHandler(route spec.Route, dispatcher Dispatcher, logger *slog.Logger) http.HandlerFunc {
	paramNames := spec.PathParamNames(route.Path)
	bindings := route.Operation.Temporal
	requestBody := route.Operation.RequestBody

	return func(w http.ResponseWriter, r *http.Request) {
		pathParams := make(map[string]string, len(paramNames))
		for _, name := range paramNames {
			pathParams[name] = r.PathValue(name)
		}

		var body any
		if r.Body != nil {
			// The error from closing a request body isn't actionable (the
			// request handling is already complete either way), but discard
			// it explicitly rather than leaving it unchecked.
			defer func() { _ = r.Body.Close() }()
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
				writeJSON(w, http.StatusUnprocessableEntity, response.Envelope{Status: response.StatusInvalidRequest, Message: "invalid JSON body: " + err.Error()})
				return
			}
		}

		if result := validateBody(requestBody, body); result.Status.IsError() {
			writeJSON(w, http.StatusUnprocessableEntity, result)
			return
		}

		resolve := fieldResolver(r, pathParams, body)

		outcomes := dispatchAll(r.Context(), dispatcher, bindings, resolve, body)
		items, statuses, succeeded := collectResults(route, bindings, outcomes, logger)

		if len(items) == 1 {
			writeJSON(w, statuses[0], items[0])
			return
		}
		writeBatchResult(w, items, statuses, succeeded)
	}
}

// dispatchAll runs dispatcher.Dispatch for every binding concurrently,
// rendering each one's workflowId template first. Running them in parallel,
// rather than stopping at the first failure, means one bad binding can't
// prevent the others in the same request from starting.
func dispatchAll(ctx context.Context, dispatcher Dispatcher, bindings []spec.TemporalBinding, resolve func(string) (string, bool), body any) []dispatchOutcome {
	outcomes := make([]dispatchOutcome, len(bindings))
	var wg sync.WaitGroup
	for i, binding := range bindings {
		wg.Add(1)
		go func(i int, binding spec.TemporalBinding) {
			defer wg.Done()
			workflowID := renderTemplate(binding.WorkflowID, resolve)
			result, err := dispatcher.Dispatch(ctx, binding, workflowID, body)
			outcomes[i] = dispatchOutcome{workflowID: workflowID, result: result, err: err}
		}(i, binding)
	}
	wg.Wait()
	return outcomes
}

// collectResults turns each dispatch outcome into its response item and
// HTTP status, logs it, and counts how many bindings actually succeeded
// (see dispatchOutcome.succeeded).
func collectResults(route spec.Route, bindings []spec.TemporalBinding, outcomes []dispatchOutcome, logger *slog.Logger) (items []any, statuses []int, succeeded int) {
	items = make([]any, len(outcomes))
	statuses = make([]int, len(outcomes))

	for i, outcome := range outcomes {
		binding := bindings[i]

		if outcome.err != nil {
			respStatus, httpStatus, message := errorResponse(outcome.err)
			statuses[i] = httpStatus
			logger.Error("temporal dispatch failed",
				"operation_id", route.Operation.OperationID,
				"method", route.Method,
				"path", route.Path,
				"workflow_id", outcome.workflowID,
				"workflow_type", binding.WorkflowType,
				"error", outcome.err,
			)
			items[i] = response.Envelope{
				Status:  respStatus,
				Message: fmt.Sprintf("%s: %s", workflowLabel(binding, outcome.workflowID), message),
			}
			continue
		}

		items[i] = outcome.result
		if outcome.succeeded() {
			succeeded++
			statuses[i] = statusByAction[binding.Action]
			if statuses[i] == 0 {
				statuses[i] = http.StatusOK
			}
			logger.Info("handled request",
				"operation_id", route.Operation.OperationID,
				"method", route.Method,
				"path", route.Path,
				"workflow_id", outcome.workflowID,
			)
			continue
		}

		// Dispatch returned no Go error, but the result itself reports a
		// non-success outcome (e.g. startWorkflow attached to an
		// already-existing run instead of creating one) - treat it the
		// same as a failure for status-code and batch-accounting purposes.
		statuses[i] = http.StatusConflict
		if sg, ok := outcome.result.(statusGetter); ok {
			logger.Info("temporal dispatch did not start a new run",
				"operation_id", route.Operation.OperationID,
				"method", route.Method,
				"path", route.Path,
				"workflow_id", outcome.workflowID,
				"status", sg.GetStatus(),
			)
		}
	}

	return items, statuses, succeeded
}

// writeBatchResult picks the top-level response.BatchResult status and HTTP
// code for a multi-binding dispatch: 207 Multi-Status only for a genuine
// mix of outcomes (see dispatchHandler's doc comment for the full rule).
func writeBatchResult(w http.ResponseWriter, items []any, statuses []int, succeeded int) {
	message := fmt.Sprintf("%d of %d workflow(s) started", succeeded, len(items))

	switch {
	case succeeded == len(items):
		status := statuses[0]
		writeJSON(w, status, response.BatchResult{
			Envelope: response.Envelope{Status: response.StatusWorkflowStarted, Message: message},
			Results:  items,
		})
	case succeeded == 0:
		// Nothing started: a uniform outcome, however many different
		// underlying reasons, so this isn't a "multi" status - report it
		// as a single failure using the first item's HTTP status.
		writeJSON(w, statuses[0], response.BatchResult{
			Envelope: response.Envelope{Status: response.StatusWorkflowNotStarted, Message: message},
			Results:  items,
		})
	default:
		// A genuine mix of outcomes: no single HTTP status code could
		// describe it, hence Multi-Status.
		writeJSON(w, http.StatusMultiStatus, response.BatchResult{
			Envelope: response.Envelope{Status: response.StatusWorkflowPartiallyStarted, Message: message},
			Results:  items,
		})
	}
}

// workflowLabel names a binding for inclusion in a failure message, so a
// batch response's per-item message identifies which workflow it's about,
// e.g. "NotificationWorkflow (order-1234-notification): ...".
func workflowLabel(binding spec.TemporalBinding, workflowID string) string {
	name := binding.WorkflowType
	if name == "" {
		name = string(binding.Action)
	}
	return fmt.Sprintf("%s (%s)", name, workflowID)
}

// errorResponse maps a Temporal service error to a response.Status, an HTTP
// status, and a message.
func errorResponse(err error) (response.Status, int, string) {
	var notFound *serviceerror.NotFound
	if errors.As(err, &notFound) {
		return response.StatusNotFound, http.StatusNotFound, err.Error()
	}

	var alreadyStarted *serviceerror.WorkflowExecutionAlreadyStarted
	if errors.As(err, &alreadyStarted) {
		return response.StatusDuplicated, http.StatusConflict, err.Error()
	}

	var invalidArgument *serviceerror.InvalidArgument
	if errors.As(err, &invalidArgument) {
		return response.StatusInvalidArgument, http.StatusBadRequest, err.Error()
	}

	var permissionDenied *serviceerror.PermissionDenied
	if errors.As(err, &permissionDenied) {
		return response.StatusForbidden, http.StatusForbidden, err.Error()
	}

	return response.StatusFailed, http.StatusBadGateway, err.Error()
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	// Once WriteHeader has been called, an Encode error here means the
	// client already disconnected - nothing left to do about it.
	_ = json.NewEncoder(w).Encode(payload)
}
