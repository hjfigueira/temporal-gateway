// Package gateway builds the HTTP handler generated from an API
// specification. Each operation becomes one route whose x-temporal bindings
// are dispatched in parallel against a real Temporal client.
package gateway

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"temporal-gateway/internal/spec"
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

// Options are the per-request limits every generated handler enforces.
// A zero field disables that limit.
type Options struct {
	// MaxBodyBytes caps the request body; a larger one gets 413.
	MaxBodyBytes int64
	// RequestTimeout bounds the Temporal dispatch (notably a blocking
	// getResult); exceeding it gets 504.
	RequestTimeout time.Duration
}

// NewHandler registers one handler per route in apiSpec, keyed by
// "METHOD /path" using Go's http.ServeMux pattern syntax. OpenAPI path
// templates ("/orders/{orderId}") already match that syntax directly.
func NewHandler(apiSpec *spec.Spec, dispatcher Dispatcher, opts Options, logger *slog.Logger) http.Handler {
	mux := http.NewServeMux()

	for _, route := range apiSpec.Routes() {
		pattern := route.Method + " " + route.Path
		mux.HandleFunc(pattern, dispatchHandler(route, dispatcher, opts, logger))
	}

	return mux
}
