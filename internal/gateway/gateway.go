// Package gateway builds the HTTP handler generated from an API
// specification. Each operation becomes one route whose x-temporal bindings
// are dispatched in parallel against a real Temporal client.
package gateway

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"temporal-gateway/internal/response"
	"temporal-gateway/internal/spec"
)

// Dispatcher executes a route's Temporal action. The Outcome's Body is
// written as the response; its Status decides success and the HTTP code.
// Implemented by *internal/temporal.Dispatcher.
type Dispatcher interface {
	Dispatch(ctx context.Context, binding spec.TemporalBinding, workflowID string, body any) (response.Outcome, error)
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
