// Package gateway builds the HTTP handler generated from an API
// specification. Each operation becomes one route whose x-temporal bindings
// are dispatched in parallel against a real Temporal client.
package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"sync"

	"github.com/google/uuid"
	"go.temporal.io/api/serviceerror"

	"temporal-gateway/internal/response"
	"temporal-gateway/internal/spec"
	"temporal-gateway/internal/validate"
)

// Dispatcher executes a route's Temporal action and returns a
// JSON-serializable result. Implemented by *internal/temporal.Dispatcher.
type Dispatcher interface {
	Dispatch(ctx context.Context, binding spec.TemporalBinding, workflowID string, body any) (any, error)
}

// statusByAction is the HTTP status returned on success, per Temporal
// action: actions that only kick off asynchronous work reply 202, actions
// that return a result reply 200.
var statusByAction = map[spec.TemporalAction]int{
	spec.ActionStartWorkflow:     http.StatusAccepted,
	spec.ActionSignalWorkflow:    http.StatusAccepted,
	spec.ActionCancelWorkflow:    http.StatusAccepted,
	spec.ActionTerminateWorkflow: http.StatusAccepted,
	spec.ActionQueryWorkflow:     http.StatusOK,
	spec.ActionGetResult:         http.StatusOK,
}

// NewHandler registers one handler per route in apiSpec, keyed by
// "METHOD /path" using Go's http.ServeMux pattern syntax. OpenAPI path
// templates ("/orders/{orderId}") already match that syntax directly.
func NewHandler(apiSpec *spec.Spec, dispatcher Dispatcher, logger *slog.Logger) http.Handler {
	mux := http.NewServeMux()

	for _, route := range apiSpec.Routes() {
		pattern := route.Method + " " + route.Path
		mux.HandleFunc(pattern, dispatchHandler(route, dispatcher, logger))
	}

	return mux
}

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
			defer r.Body.Close()
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
				writeJSON(w, http.StatusBadRequest, response.Envelope{Status: response.StatusInvalidRequest, Message: "invalid JSON body: " + err.Error()})
				return
			}
		}

		if result := validateBody(requestBody, body); result.Status.IsError() {
			writeJSON(w, http.StatusBadRequest, result)
			return
		}

		resolve := fieldResolver(r, pathParams, body)

		outcomes := make([]dispatchOutcome, len(bindings))
		var wg sync.WaitGroup
		for i, binding := range bindings {
			wg.Add(1)
			go func(i int, binding spec.TemporalBinding) {
				defer wg.Done()
				workflowID := renderTemplate(binding.WorkflowID, resolve)
				result, err := dispatcher.Dispatch(r.Context(), binding, workflowID, body)
				outcomes[i] = dispatchOutcome{workflowID: workflowID, result: result, err: err}
			}(i, binding)
		}
		wg.Wait()

		items := make([]any, len(outcomes))
		statuses := make([]int, len(outcomes))
		succeeded := 0
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

			// Dispatch returned no Go error, but the result itself reports
			// a non-success outcome (e.g. startWorkflow attached to an
			// already-existing run instead of creating one) - treat it the
			// same as a failure for status-code and batch-accounting
			// purposes.
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

		if len(items) == 1 {
			writeJSON(w, statuses[0], items[0])
			return
		}

		switch {
		case succeeded == len(items):
			status := statusByAction[bindings[0].Action]
			if status == 0 {
				status = http.StatusOK
			}
			writeJSON(w, status, response.BatchResult{
				Envelope: response.Envelope{
					Status:  response.StatusWorkflowStarted,
					Message: fmt.Sprintf("%d of %d workflow(s) started", succeeded, len(items)),
				},
				Results: items,
			})
		case succeeded == 0:
			// Nothing started: a uniform outcome, however many different
			// underlying reasons, so this isn't a "multi" status - report
			// it as a single failure using the first item's HTTP status.
			writeJSON(w, statuses[0], response.BatchResult{
				Envelope: response.Envelope{
					Status:  response.StatusWorkflowNotStarted,
					Message: fmt.Sprintf("%d of %d workflow(s) started", succeeded, len(items)),
				},
				Results: items,
			})
		default:
			// A genuine mix of outcomes: no single HTTP status code could
			// describe it, hence Multi-Status.
			writeJSON(w, http.StatusMultiStatus, response.BatchResult{
				Envelope: response.Envelope{
					Status:  response.StatusWorkflowPartiallyStarted,
					Message: fmt.Sprintf("%d of %d workflow(s) started", succeeded, len(items)),
				},
				Results: items,
			})
		}
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

// validateBody enforces the operation's requestBody.required flag and, when
// an application/json schema is declared, validates body against it. The
// returned Result.Status.IsError() is false when there's nothing to report.
func validateBody(rb *spec.RequestBody, body any) validate.Result {
	if rb == nil {
		return validate.Result{Envelope: response.Envelope{Status: response.StatusValid}}
	}
	if body == nil {
		if rb.Required {
			return validate.Result{Envelope: response.Envelope{Status: response.StatusInvalidRequest, Message: "request body is required"}}
		}
		return validate.Result{Envelope: response.Envelope{Status: response.StatusValid}}
	}
	media, ok := rb.Content["application/json"]
	if !ok || len(media.Schema) == 0 {
		return validate.Result{Envelope: response.Envelope{Status: response.StatusValid}}
	}
	return validate.Schema(media.Schema, body)
}

// templatePlaceholder matches a "{name}" placeholder in a workflowId
// template.
var templatePlaceholder = regexp.MustCompile(`\{([^{}]+)\}`)

// renderTemplate substitutes "{name}" placeholders in tmpl using resolve,
// e.g. renderTemplate("order-{path.orderId}", resolve) -> "order-o1" when
// resolve("path.orderId") returns ("o1", true). A placeholder resolve can't
// satisfy is left untouched.
func renderTemplate(tmpl string, resolve func(name string) (string, bool)) string {
	return templatePlaceholder.ReplaceAllStringFunc(tmpl, func(match string) string {
		name := match[1 : len(match)-1]
		if value, ok := resolve(name); ok {
			return value
		}
		return match
	})
}

// fieldResolver returns a lookup used to fill "{name}" placeholders in a
// workflowId template. name is either the reserved "uuidv7" keyword, which
// generates a fresh UUIDv7 per occurrence, or an "origin.field" reference
// naming which part of the request field comes from: "path", "body",
// "query", or "header".
func fieldResolver(r *http.Request, pathParams map[string]string, body any) func(name string) (string, bool) {
	bodyFields, _ := body.(map[string]any)
	query := r.URL.Query()

	return func(name string) (string, bool) {
		if strings.EqualFold(name, "uuidv7") {
			id, err := uuid.NewV7()
			if err != nil {
				return "", false
			}
			return id.String(), true
		}

		origin, field, ok := strings.Cut(name, ".")
		if !ok {
			return "", false
		}

		switch strings.ToLower(origin) {
		case "path":
			value, ok := pathParams[field]
			return value, ok
		case "body":
			value, ok := bodyFields[field]
			if !ok {
				return "", false
			}
			return stringifyField(value), true
		case "query":
			values, ok := query[field]
			if !ok || len(values) == 0 {
				return "", false
			}
			return values[0], true
		case "header":
			values, ok := r.Header[http.CanonicalHeaderKey(field)]
			if !ok || len(values) == 0 {
				return "", false
			}
			return values[0], true
		default:
			return "", false
		}
	}
}

// stringifyField renders a decoded JSON body value as a string for use in a
// workflowId template. JSON objects and arrays are not meaningful workflow
// ID components, so they render as their Go-syntax representation rather
// than being rejected outright.
func stringifyField(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case nil:
		return ""
	default:
		return fmt.Sprint(t)
	}
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
	json.NewEncoder(w).Encode(payload)
}
