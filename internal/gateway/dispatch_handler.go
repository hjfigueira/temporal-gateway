package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"slices"
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
	result     response.Outcome
	err        error
}

// rejection is a request turned away before anything was dispatched.
type rejection struct {
	code   int
	body   any
	reason string // the span's error status
	err    error  // recorded on the span when set
}

func (rej *rejection) write(w http.ResponseWriter, span trace.Span) {
	if rej.err != nil {
		span.RecordError(rej.err)
	}
	span.SetStatus(codes.Error, rej.reason)
	writeJSON(w, rej.code, rej.body)
}

// dispatchHandler reads and validates the request against its OpenAPI
// operation, renders every trigger's workflowId, then dispatches the
// operation's x-temporal.triggers in parallel (see dispatchAll). One
// trigger reports its own result; more than one report a
// response.BatchResult (see writeBatchResult).
func dispatchHandler(route spec.Route, dispatcher Dispatcher, opts Options, logger *slog.Logger) http.HandlerFunc {
	paramNames := spec.PathParamNames(route.Path)
	bindings := route.Operation.Temporal.Triggers
	returnStrategy := route.Operation.Temporal.Strategy()
	vocab := batchVocabularyFor(bindings)

	return func(w http.ResponseWriter, r *http.Request) {
		ctx, span, requestLogger := startRequestSpan(r, route, logger)
		defer span.End()

		pathParams := make(map[string]string, len(paramNames))
		for _, name := range paramNames {
			pathParams[name] = r.PathValue(name)
		}

		raw, body, rej := readBody(w, r, opts.MaxBodyBytes)
		if rej != nil {
			rej.write(w, span)
			return
		}
		if rej := checkRequest(ctx, route, r, raw, pathParams, requestLogger); rej != nil {
			rej.write(w, span)
			return
		}
		workflowIDs, rej := renderWorkflowIDs(bindings, templating.Source{PathParams: pathParams, Query: r.URL.Query(), Header: r.Header, Body: body})
		if rej != nil {
			rej.write(w, span)
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
		writeBatchResult(w, items, statuses, succeeded, returnStrategy, vocab)
	}
}

// startRequestSpan starts the request's first span, and a logger carrying
// its trace/span IDs. The trace context flows through dispatchAll into
// dispatcher.Dispatch and on to client.ExecuteWorkflow/SignalWorkflow/etc,
// where go.temporal.io/sdk/contrib/opentelemetry's interceptor (see
// internal/temporal.NewClient) propagates it into the Temporal request's
// Header - so the workflow this request dispatches continues the same
// trace.
func startRequestSpan(r *http.Request, route spec.Route, logger *slog.Logger) (context.Context, trace.Span, *slog.Logger) {
	ctx, span := tracer.Start(r.Context(), route.Operation.OperationID,
		trace.WithAttributes(
			attribute.String("temporal_gateway.operation_id", route.Operation.OperationID),
			attribute.String("http.method", route.Method),
			attribute.String("http.route", route.Path),
		),
	)
	// A no-op span (telemetry disabled) has an invalid SpanContext, so logs
	// stay unchanged in that case.
	if sc := span.SpanContext(); sc.IsValid() {
		logger = logger.With("trace_id", sc.TraceID().String(), "span_id", sc.SpanID().String())
	}
	return ctx, span, logger
}

// readBody reads the request body, capped at maxBytes (0 = no cap), and
// decodes it as JSON. It returns the raw bytes too, for validateRequest.
func readBody(w http.ResponseWriter, r *http.Request, maxBytes int64) ([]byte, any, *rejection) {
	if r.Body == nil {
		return nil, nil, nil
	}
	// The error from closing a request body isn't actionable (the request
	// handling is already complete either way), but discard it explicitly
	// rather than leaving it unchecked.
	defer func() { _ = r.Body.Close() }()
	if maxBytes > 0 {
		r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	}
	raw, err := io.ReadAll(r.Body)
	var body any
	if err == nil {
		body, err = decodeBody(bytes.NewReader(raw))
	}
	if err == nil {
		return raw, body, nil
	}
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		return nil, nil, &rejection{
			code:   http.StatusRequestEntityTooLarge,
			body:   response.Envelope{Status: response.StatusPayloadTooLarge, Message: fmt.Sprintf("request body exceeds %d bytes", tooLarge.Limit)},
			reason: "request body too large",
			err:    err,
		}
	}
	return nil, nil, &rejection{
		code:   http.StatusUnprocessableEntity,
		body:   response.Envelope{Status: response.StatusInvalidRequest, Message: "invalid JSON body: " + err.Error()},
		reason: "invalid JSON body",
		err:    err,
	}
}

// checkRequest validates the request against its OpenAPI operation (see
// validateRequest), rejecting it with every problem found.
func checkRequest(ctx context.Context, route spec.Route, r *http.Request, raw []byte, pathParams map[string]string, logger *slog.Logger) *rejection {
	fields := validateRequest(ctx, route.OpenAPI, r, raw, pathParams)
	if fields == nil {
		return nil
	}
	// Field names only: the values may be sensitive.
	logger.Info("request failed validation",
		"operation_id", route.Operation.OperationID,
		"method", route.Method,
		"path", route.Path,
		"fields", slices.Sorted(maps.Keys(fields)),
	)
	issues := 0
	for _, messages := range fields {
		issues += len(messages)
	}
	return &rejection{
		code: http.StatusUnprocessableEntity,
		body: response.ValidationFailed{
			Envelope: response.Envelope{Status: response.StatusValidationFailed, Message: fmt.Sprintf("validation failed: %d issue(s) found", issues)},
			Fields:   fields,
		},
		reason: string(response.StatusValidationFailed),
	}
}

// renderWorkflowIDs renders every binding's workflowId before any is
// dispatched, so a request missing a templated field dispatches nothing
// rather than acting on a literal "{...}" ID shared by every such request.
func renderWorkflowIDs(bindings []spec.TemporalBinding, src templating.Source) ([]string, *rejection) {
	workflowIDs := make([]string, len(bindings))
	var unresolved []string
	for i, binding := range bindings {
		var missing []string
		workflowIDs[i], missing = templating.Render(binding.WorkflowID, src)
		unresolved = append(unresolved, missing...)
	}
	if len(unresolved) > 0 {
		return nil, &rejection{
			code:   http.StatusUnprocessableEntity,
			body:   response.Envelope{Status: response.StatusInvalidRequest, Message: "request is missing values for workflowId placeholder(s): " + strings.Join(unresolved, ", ")},
			reason: "unresolved workflowId placeholder",
		}
	}
	return workflowIDs, nil
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
			outcome.result, outcome.err = response.Outcome{}, fmt.Errorf("dispatch panicked: %v", r)
		}
	}()
	outcome.result, outcome.err = dispatcher.Dispatch(ctx, binding, workflowID, body)
	return outcome
}

// collectResults turns each dispatch outcome into its response item and
// HTTP status, logs it, and counts how many bindings actually succeeded
// (see response.Outcome.Succeeded).
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

		items[i] = outcome.result.Body
		if outcome.result.Succeeded() {
			succeeded++
			statuses[i] = successStatus(outcome.result)
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
		logger.Info("temporal dispatch did not start a new run",
			"operation_id", route.Operation.OperationID,
			"method", route.Method,
			"path", route.Path,
			"workflow_id", outcome.workflowID,
			"status", outcome.result.Status,
		)
	}

	return items, statuses, succeeded
}

// successStatus is the HTTP status for a succeeded outcome: 200 for an
// action's raw result (queryWorkflow, getResult), 202 for an
// acknowledgement of work Temporal carries on asynchronously.
func successStatus(o response.Outcome) int {
	if o.IsRaw() {
		return http.StatusOK
	}
	return http.StatusAccepted
}

// batchVocabulary names a multi-trigger outcome's top-level statuses.
type batchVocabulary struct {
	all, partial, none response.Status
	noun               string // completes "N of M ..."
}

var (
	startBatch = batchVocabulary{response.StatusWorkflowStarted, response.StatusWorkflowPartiallyStarted, response.StatusWorkflowNotStarted, "workflow(s) started"}
	mixedBatch = batchVocabulary{response.StatusBatchSucceeded, response.StatusBatchPartiallySucceeded, response.StatusBatchFailed, "trigger(s) succeeded"}
)

// batchVocabularyFor speaks of workflows started only when every binding
// starts one, so e.g. a signal fan-out doesn't claim "WORKFLOW_STARTED".
func batchVocabularyFor(bindings []spec.TemporalBinding) batchVocabulary {
	for _, b := range bindings {
		if b.Action != spec.ActionStartWorkflow {
			return mixedBatch
		}
	}
	return startBatch
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
func writeBatchResult(w http.ResponseWriter, items []any, statuses []int, succeeded int, strategy spec.ReturnStrategy, vocab batchVocabulary) {
	total := len(items)
	message := fmt.Sprintf("%d of %d %s", succeeded, total, vocab.noun)
	write := func(code int, status response.Status) {
		writeJSON(w, code, response.BatchResult{
			Envelope: response.Envelope{Status: status, Message: message},
			Results:  items,
		})
	}

	switch {
	case succeeded == total:
		write(statuses[0], vocab.all)
	case strategy == spec.ReturnStrategyAllOrNothing:
		write(http.StatusConflict, vocab.none)
	case succeeded == 0:
		write(statuses[0], vocab.none)
	default:
		write(http.StatusMultiStatus, vocab.partial)
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
