// Package gateway builds the HTTP handler generated from an API
// specification. Each operation becomes one route whose x-temporal bindings
// are dispatched in parallel against a real Temporal client.
package gateway

import (
	"context"
	"log/slog"
	"net/http"

	"temporal-gateway/internal/spec"
)

// Dispatcher executes a route's Temporal action and returns a
// JSON-serializable result. Implemented by *internal/temporal.Dispatcher.
type Dispatcher interface {
	Dispatch(ctx context.Context, binding spec.TemporalBinding, workflowID string, body any) (any, error)
}

// Driver builds the HTTP handler for a single route under whichever
// x-temporal.driver strategy it selects. DirectDriver (every trigger
// dispatched straight to Temporal, concurrently) and NexusDriver (one
// CascadeEvent workflow started in their place, see
// internal/temporal.CascadeEvent) are the two implementations; NewHandler
// picks between them per operation via spec.TemporalSpec.DriverOrDefault -
// isolating the two behind this interface is what lets a spec mix both
// across different routes, or swap one for the other by editing the spec
// alone.
type Driver interface {
	Handler(route spec.Route, logger *slog.Logger) http.HandlerFunc
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
// templates ("/orders/{orderId}") already match that syntax directly. Each
// route's handler comes from direct or nexus depending on its operation's
// x-temporal.driver (see Driver).
func NewHandler(apiSpec *spec.Spec, direct, nexus Driver, logger *slog.Logger) http.Handler {
	mux := http.NewServeMux()

	for _, route := range apiSpec.Routes() {
		driver := direct
		if route.Operation.Temporal.DriverOrDefault() == spec.DriverNexus {
			driver = nexus
		}
		pattern := route.Method + " " + route.Path
		mux.HandleFunc(pattern, driver.Handler(route, logger))
	}

	return mux
}
