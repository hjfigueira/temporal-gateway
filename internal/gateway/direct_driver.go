package gateway

import (
	"log/slog"
	"net/http"

	"temporal-gateway/internal/spec"
)

// DirectDriver implements Driver for the default x-temporal.driver,
// "direct": every trigger is dispatched straight to Temporal, concurrently
// - see dispatchHandler, which does the actual work.
type DirectDriver struct {
	Dispatcher Dispatcher
}

func (d DirectDriver) Handler(route spec.Route, logger *slog.Logger) http.HandlerFunc {
	return dispatchHandler(route, d.Dispatcher, logger)
}
