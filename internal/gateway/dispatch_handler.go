package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"go.temporal.io/api/serviceerror"

	"temporal-gateway/internal/response"
	"temporal-gateway/internal/spec"
	"temporal-gateway/internal/templating"
)

// tracer starts the one span dispatchHandler creates per HTTP request. It
// resolves against whatever TracerProvider is globally configured when each
// span starts (see internal/telemetry.Setup); with telemetry disabled, the
// global provider is a no-op, so these spans cost effectively nothing.
var tracer = otel.Tracer("temporal-gateway")

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
// which is a Status.IsError() outcome despite there being no error. Only
// called for outcomes whose err is nil (see collectResults).
func (o dispatchOutcome) succeeded() bool {
	if sg, ok := o.result.(statusGetter); ok {
		return !sg.GetStatus().IsError()
	}
	return true
}

// dispatchHandler decodes the request, validates it against its OpenAPI
// operation (see validateRequest), renders every trigger's workflowId, then
// dispatches the operation's x-temporal.triggers in parallel (see
// dispatchAll). One trigger reports its own result; more
// than one report a response.BatchResult (see writeBatchResult).
func dispatchHandler(route spec.Route, dispatcher Dispatcher, opts Options, logger *slog.Logger) http.HandlerFunc {
	paramNames := spec.PathParamNames(route.Path)
	bindings := route.Operation.Temporal.Triggers
	returnStrategy := route.Operation.Temporal.Strategy()

	return func(w http.ResponseWriter, r *http.Request) {
		// This is the first span for the request: its trace context flows
		// through dispatchAll into dispatcher.Dispatch and on to
		// client.ExecuteWorkflow/SignalWorkflow/etc, where
		// go.temporal.io/sdk/contrib/opentelemetry's interceptor (see
		// internal/temporal.NewClient) propagates it into the Temporal
		// request's Header - so the workflow this request dispatches
		// continues the same trace.
		ctx, span := tracer.Start(r.Context(), route.Operation.OperationID,
			trace.WithAttributes(
				attribute.String("temporal_gateway.operation_id", route.Operation.OperationID),
				attribute.String("http.method", route.Method),
				attribute.String("http.route", route.Path),
			),
		)
		defer span.End()

		// Enrich the request's logs with the span's identifiers so they can
		// be correlated with the trace in the OTel backend. A no-op span
		// (telemetry disabled) has an invalid SpanContext, so logs stay
		// unchanged in that case.
		requestLogger := logger
		if sc := span.SpanContext(); sc.IsValid() {
			requestLogger = logger.With("trace_id", sc.TraceID().String(), "span_id", sc.SpanID().String())
		}

		pathParams := make(map[string]string, len(paramNames))
		for _, name := range paramNames {
			pathParams[name] = r.PathValue(name)
		}

		var raw []byte
		var body any
		if r.Body != nil {
			// The error from closing a request body isn't actionable (the
			// request handling is already complete either way), but discard
			// it explicitly rather than leaving it unchecked.
			defer func() { _ = r.Body.Close() }()
			if opts.MaxBodyBytes > 0 {
				r.Body = http.MaxBytesReader(w, r.Body, opts.MaxBodyBytes)
			}
			var err error
			if raw, err = io.ReadAll(r.Body); err == nil {
				body, err = decodeBody(bytes.NewReader(raw))
			}
			if err != nil {
				span.RecordError(err)
				var tooLarge *http.MaxBytesError
				if errors.As(err, &tooLarge) {
					span.SetStatus(codes.Error, "request body too large")
					writeJSON(w, http.StatusRequestEntityTooLarge, response.Envelope{Status: response.StatusPayloadTooLarge, Message: fmt.Sprintf("request body exceeds %d bytes", tooLarge.Limit)})
					return
				}
				span.SetStatus(codes.Error, "invalid JSON body")
				writeJSON(w, http.StatusUnprocessableEntity, response.Envelope{Status: response.StatusInvalidRequest, Message: "invalid JSON body: " + err.Error()})
				return
			}
		}

		if fields := validateRequest(ctx, route.OpenAPI, r, raw, pathParams); fields != nil {
			span.SetStatus(codes.Error, string(response.StatusValidationFailed))
			issues := 0
			for _, messages := range fields {
				issues += len(messages)
			}
			writeJSON(w, http.StatusUnprocessableEntity, response.ValidationFailed{
				Envelope: response.Envelope{Status: response.StatusValidationFailed, Message: fmt.Sprintf("validation failed: %d issue(s) found", issues)},
				Fields:   fields,
			})
			return
		}

		// Render every workflowId before dispatching any, so a request
		// missing a templated field dispatches nothing rather than acting
		// on a literal "{...}" ID shared by every such request.
		src := templating.Source{PathParams: pathParams, Query: r.URL.Query(), Header: r.Header, Body: body}
		workflowIDs := make([]string, len(bindings))
		var unresolved []string
		for i, binding := range bindings {
			var missing []string
			workflowIDs[i], missing = templating.Render(binding.WorkflowID, src)
			unresolved = append(unresolved, missing...)
		}
		if len(unresolved) > 0 {
			span.SetStatus(codes.Error, "unresolved workflowId placeholder")
			writeJSON(w, http.StatusUnprocessableEntity, response.Envelope{Status: response.StatusInvalidRequest, Message: "request is missing values for workflowId placeholder(s): " + strings.Join(unresolved, ", ")})
			return
		}

		if opts.RequestTimeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, opts.RequestTimeout)
			defer cancel()
		}

		outcomes := dispatchAll(ctx, dispatcher, bindings, workflowIDs, body)
		items, statuses, succeeded := collectResults(route, bindings, outcomes, requestLogger)

		span.SetAttributes(
			attribute.Int("temporal_gateway.bindings_dispatched", len(bindings)),
			attribute.Int("temporal_gateway.bindings_succeeded", succeeded),
		)
		if succeeded < len(bindings) {
			span.SetStatus(codes.Error, "one or more x-temporal bindings did not succeed")
		}

		if len(items) == 1 {
			writeJSON(w, statuses[0], items[0])
			return
		}
		writeBatchResult(w, items, statuses, succeeded, returnStrategy)
	}
}

// decodeBody decodes a JSON request body, or returns nil for an empty one.
// Numbers stay json.Number, so an ID like 12345678901234567 reaches
// workflow IDs and workflow inputs exactly, not rounded through float64
// (ADR-028). Anything after the first JSON value is an error.
func decodeBody(r io.Reader) (any, error) {
	dec := json.NewDecoder(r)
	dec.UseNumber()
	var body any
	if err := dec.Decode(&body); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, nil
		}
		return nil, err
	}
	if err := dec.Decode(new(any)); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("unexpected data after the JSON value")
		}
		return nil, err
	}
	return body, nil
}

// dispatchAll runs dispatcher.Dispatch for every binding concurrently, each
// with its already-rendered workflowIDs[i], so one bad binding can't
// prevent the others in the same request from starting. A single binding
// is dispatched inline.
func dispatchAll(ctx context.Context, dispatcher Dispatcher, bindings []spec.TemporalBinding, workflowIDs []string, body any) []dispatchOutcome {
	outcomes := make([]dispatchOutcome, len(bindings))
	if len(bindings) == 1 {
		outcomes[0] = dispatchOne(ctx, dispatcher, bindings[0], workflowIDs[0], body)
		return outcomes
	}

	var wg sync.WaitGroup
	for i, binding := range bindings {
		wg.Go(func() {
			outcomes[i] = dispatchOne(ctx, dispatcher, binding, workflowIDs[i], body)
		})
	}
	wg.Wait()
	return outcomes
}

// dispatchOne runs one Dispatch, turning a panic into that binding's error:
// net/http only recovers panics on the handler's own goroutine, so one in a
// fan-out goroutine would otherwise crash the whole gateway.
func dispatchOne(ctx context.Context, dispatcher Dispatcher, binding spec.TemporalBinding, workflowID string, body any) (outcome dispatchOutcome) {
	outcome.workflowID = workflowID
	defer func() {
		if r := recover(); r != nil {
			outcome.result, outcome.err = nil, fmt.Errorf("dispatch panicked: %v", r)
		}
	}()
	outcome.result, outcome.err = dispatcher.Dispatch(ctx, binding, workflowID, body)
	return outcome
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
// code for a multi-trigger dispatch. Every trigger succeeding is always
// reported the same way, regardless of strategy. Short of that:
//
//   - ReturnStrategyAllOrNothing treats a partial mix the same as a total
//     failure - at least one trigger didn't complete, so the whole operation
//     is rejected with 409 Conflict.
//   - ReturnStrategyAcceptPartial (the default) reports a genuine mix (at
//     least one succeeded, at least one didn't) as 207 Multi-Status, so the
//     caller can see exactly which triggers succeeded; a uniform failure
//     (none did) isn't a "multi" status, so it gets an ordinary single
//     status code from the first item instead.
func writeBatchResult(w http.ResponseWriter, items []any, statuses []int, succeeded int, strategy spec.ReturnStrategy) {
	total := len(items)
	message := fmt.Sprintf("%d of %d workflow(s) started", succeeded, total)

	if succeeded == total {
		writeJSON(w, statuses[0], response.BatchResult{
			Envelope: response.Envelope{Status: response.StatusWorkflowStarted, Message: message},
			Results:  items,
		})
		return
	}

	if strategy == spec.ReturnStrategyAllOrNothing {
		writeJSON(w, http.StatusConflict, response.BatchResult{
			Envelope: response.Envelope{Status: response.StatusWorkflowNotStarted, Message: message},
			Results:  items,
		})
		return
	}

	if succeeded == 0 {
		writeJSON(w, statuses[0], response.BatchResult{
			Envelope: response.Envelope{Status: response.StatusWorkflowNotStarted, Message: message},
			Results:  items,
		})
		return
	}

	writeJSON(w, http.StatusMultiStatus, response.BatchResult{
		Envelope: response.Envelope{Status: response.StatusWorkflowPartiallyStarted, Message: message},
		Results:  items,
	})
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

// errorResponse maps a dispatch error to a response.Status, an HTTP status,
// and a fixed client-facing message. The raw error can name namespaces,
// hosts, or server internals, so it's only logged (see collectResults),
// never returned.
func errorResponse(err error) (response.Status, int, string) {
	var notFound *serviceerror.NotFound
	if errors.As(err, &notFound) {
		return response.StatusNotFound, http.StatusNotFound, "workflow not found"
	}

	var alreadyStarted *serviceerror.WorkflowExecutionAlreadyStarted
	if errors.As(err, &alreadyStarted) {
		return response.StatusDuplicated, http.StatusConflict, "workflow already started"
	}

	var invalidArgument *serviceerror.InvalidArgument
	if errors.As(err, &invalidArgument) {
		return response.StatusInvalidArgument, http.StatusBadRequest, "rejected by temporal: invalid argument"
	}

	var permissionDenied *serviceerror.PermissionDenied
	if errors.As(err, &permissionDenied) {
		return response.StatusForbidden, http.StatusForbidden, "permission denied"
	}

	var unavailable *serviceerror.Unavailable
	if errors.As(err, &unavailable) {
		return response.StatusUnavailable, http.StatusServiceUnavailable, "temporal unavailable"
	}

	var deadlineExceeded *serviceerror.DeadlineExceeded
	if errors.Is(err, context.DeadlineExceeded) || errors.As(err, &deadlineExceeded) {
		return response.StatusTimeout, http.StatusGatewayTimeout, "timed out waiting for temporal"
	}

	return response.StatusFailed, http.StatusBadGateway, "temporal request failed"
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	// Once WriteHeader has been called, an Encode error here means the
	// client already disconnected - nothing left to do about it.
	_ = json.NewEncoder(w).Encode(payload)
}
