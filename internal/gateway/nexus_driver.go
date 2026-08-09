package gateway

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"temporal-gateway/internal/response"
	"temporal-gateway/internal/spec"
)

// NexusDriver implements Driver for x-temporal.driver: nexus: instead of
// dispatching an operation's triggers directly, it starts the CascadeEvent
// workflow named by x-temporal.config, passing the triggers - rendered
// against the incoming request - as its input. See nexusDispatchHandler for
// the actual per-request logic, and internal/temporal.CascadeEvent for what
// happens to those triggers once the workflow picks them up.
type NexusDriver struct {
	Dispatcher Dispatcher
	// NexusEndpoints maps a namespace to the Nexus endpoint that reaches it
	// (config.TemporalConnectionConfig.NexusEndpoint) - see
	// internal/temporal.Connections.NexusEndpoints.
	NexusEndpoints map[string]string
}

func (n NexusDriver) Handler(route spec.Route, logger *slog.Logger) http.HandlerFunc {
	return nexusDispatchHandler(route, n.Dispatcher, n.NexusEndpoints, logger)
}

// nexusDispatchHandler is NexusDriver's counterpart to dispatchHandler: it
// starts exactly one workflow - the CascadeEvent workflow named by
// x-temporal.config (see spec.NexusConfig) - passing the operation's
// triggers, already rendered against the incoming request, as its input
// (see spec.CascadeInput). That workflow, running on a worker this gateway
// also hosts (see internal/temporal.BuildNexusWorkers), fans them out over
// Nexus to whichever namespace each one targets. This handler reports only
// the CascadeEvent workflow's own start outcome - it can't know
// synchronously whether the workflows listed under triggers actually
// started, since that happens inside CascadeEvent, asynchronously from
// this HTTP call.
func nexusDispatchHandler(route spec.Route, dispatcher Dispatcher, nexusEndpoints map[string]string, logger *slog.Logger) http.HandlerFunc {
	paramNames := spec.PathParamNames(route.Path)
	cfg := route.Operation.Temporal.Config
	triggers := route.Operation.Temporal.Triggers
	requestBody := route.Operation.RequestBody

	binding := spec.TemporalBinding{
		Action:       spec.ActionStartWorkflow,
		Namespace:    cfg.Namespace,
		WorkflowType: cfg.WorkflowTypeOrDefault(),
		TaskQueue:    cfg.TaskQueue,
	}

	return func(w http.ResponseWriter, r *http.Request) {
		ctx, span := tracer.Start(r.Context(), route.Operation.OperationID,
			trace.WithAttributes(
				attribute.String("temporal_gateway.operation_id", route.Operation.OperationID),
				attribute.String("http.method", route.Method),
				attribute.String("http.route", route.Path),
				attribute.String("temporal_gateway.driver", string(spec.DriverNexus)),
			),
		)
		defer span.End()

		requestLogger := logger
		if sc := span.SpanContext(); sc.IsValid() {
			requestLogger = logger.With("trace_id", sc.TraceID().String(), "span_id", sc.SpanID().String())
		}

		pathParams := make(map[string]string, len(paramNames))
		for _, name := range paramNames {
			pathParams[name] = r.PathValue(name)
		}

		var body any
		if r.Body != nil {
			defer func() { _ = r.Body.Close() }()
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
				span.RecordError(err)
				span.SetStatus(codes.Error, "invalid JSON body")
				writeJSON(w, http.StatusUnprocessableEntity, response.Envelope{Status: response.StatusInvalidRequest, Message: "invalid JSON body: " + err.Error()})
				return
			}
		}

		if result := validateBody(requestBody, body); result.Status.IsError() {
			span.SetStatus(codes.Error, string(result.Status))
			writeJSON(w, http.StatusUnprocessableEntity, result)
			return
		}

		resolve := fieldResolver(r, pathParams, body)

		cascadeBinding := binding
		cascadeBinding.WorkflowID = renderTemplate(cfg.WorkflowID, resolve)
		cascadeInput := spec.CascadeInput{
			Body:     body,
			Triggers: renderCascadeTriggers(triggers, resolve, nexusEndpoints),
		}

		result, err := dispatcher.Dispatch(ctx, cascadeBinding, cascadeBinding.WorkflowID, cascadeInput)
		item, status, ok := resultItem(route, cascadeBinding, cascadeBinding.WorkflowID, result, err, requestLogger)

		span.SetAttributes(attribute.Int("temporal_gateway.triggers_cascaded", len(triggers)))
		if !ok {
			span.SetStatus(codes.Error, "cascade workflow did not start")
		}

		writeJSON(w, status, item)
	}
}

// renderCascadeTriggers renders every trigger's WorkflowID against the
// incoming request (a workflow has no access to that request, so this has
// to happen gateway-side, before the trigger is handed to CascadeEvent as
// input) and attaches the Nexus endpoint that reaches its target namespace.
func renderCascadeTriggers(triggers []spec.TemporalBinding, resolve func(string) (string, bool), nexusEndpoints map[string]string) []spec.CascadeTrigger {
	rendered := make([]spec.CascadeTrigger, len(triggers))
	for i, t := range triggers {
		t.WorkflowID = renderTemplate(t.WorkflowID, resolve)
		rendered[i] = spec.CascadeTrigger{TemporalBinding: t, NexusEndpoint: nexusEndpoints[t.Namespace]}
	}
	return rendered
}
