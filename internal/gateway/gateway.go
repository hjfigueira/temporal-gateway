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

// dispatchHandler resolves path params and the JSON body, then renders and
// dispatches each of the operation's x-temporal bindings in parallel, so
// one HTTP call can trigger several Temporal actions at once and a failure
// in one binding doesn't prevent the others from starting.
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
				writeJSON(w, http.StatusBadRequest, response.Envelope{Success: false, Message: "invalid JSON body: " + err.Error()})
				return
			}
		}

		if result := validateBody(requestBody, body); !result.Success {
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

		results := make([]any, 0, len(bindings))
		for _, outcome := range outcomes {
			if outcome.err != nil {
				logger.Error("temporal dispatch failed",
					"operation_id", route.Operation.OperationID,
					"method", route.Method,
					"path", route.Path,
					"workflow_id", outcome.workflowID,
					"error", outcome.err,
				)
				status, message := errorResponse(outcome.err)
				writeJSON(w, status, response.Envelope{Success: false, Message: message})
				return
			}

			logger.Info("handled request",
				"operation_id", route.Operation.OperationID,
				"method", route.Method,
				"path", route.Path,
				"workflow_id", outcome.workflowID,
			)
			results = append(results, outcome.result)
		}

		status := statusByAction[bindings[0].Action]
		if status == 0 {
			status = http.StatusOK
		}
		if len(results) == 1 {
			writeJSON(w, status, results[0])
			return
		}
		writeJSON(w, status, results)
	}
}

// validateBody enforces the operation's requestBody.required flag and, when
// an application/json schema is declared, validates body against it. The
// returned Result.Success is true when there's nothing to report.
func validateBody(rb *spec.RequestBody, body any) validate.Result {
	if rb == nil {
		return validate.Result{Envelope: response.Envelope{Success: true}}
	}
	if body == nil {
		if rb.Required {
			return validate.Result{Envelope: response.Envelope{Success: false, Message: "request body is required"}}
		}
		return validate.Result{Envelope: response.Envelope{Success: true}}
	}
	media, ok := rb.Content["application/json"]
	if !ok || len(media.Schema) == 0 {
		return validate.Result{Envelope: response.Envelope{Success: true}}
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

// errorResponse maps a Temporal service error to an HTTP status and message.
func errorResponse(err error) (int, string) {
	var notFound *serviceerror.NotFound
	if errors.As(err, &notFound) {
		return http.StatusNotFound, err.Error()
	}

	var alreadyStarted *serviceerror.WorkflowExecutionAlreadyStarted
	if errors.As(err, &alreadyStarted) {
		return http.StatusConflict, err.Error()
	}

	var invalidArgument *serviceerror.InvalidArgument
	if errors.As(err, &invalidArgument) {
		return http.StatusBadRequest, err.Error()
	}

	var permissionDenied *serviceerror.PermissionDenied
	if errors.As(err, &permissionDenied) {
		return http.StatusForbidden, err.Error()
	}

	return http.StatusBadGateway, err.Error()
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(payload)
}
