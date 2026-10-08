package bdd

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"

	"temporal-gateway/internal/gateway"
	"temporal-gateway/internal/response"
	"temporal-gateway/internal/spec"
)

// world is the single piece of mutable state shared by every step
// definition in this suite. godog gives every scenario a fresh pass
// through sc.Before (see InitializeScenario), which calls reset - so
// scenarios never leak state into one another despite sharing one *world
// instance across the whole suite run.
//
// It's one struct, rather than one per feature file, because a few steps
// (building a route, firing a request, decoding the response) are needed
// by more than one feature file verbatim - see the shared fakes/HTTP
// plumbing below and steps_spec_loading_test.go.
type world struct {
	// ---- last HTTP round-trip (shared by every feature file) ----
	handler  http.Handler
	rec      *httptest.ResponseRecorder
	decoded  map[string]any // last response body, decoded generically
	rawSpec  string         // raw YAML text, when a scenario builds the spec from YAML
	specErr  error          // result of the last spec.Load
	loadedS  *spec.Spec
	routesOf []spec.Route // result of the last Spec.Routes() call

	// ---- workflow-id-templating.feature ----
	tmpl        string
	pathParams  map[string]string
	bodyFields  map[string]any
	queryParams map[string][]string
	headers     map[string][]string
	renderedIDs []string
	lastStatus  int

	// ---- request-validation.feature ----
	schema              map[string]any
	requestBodyRequired bool
	validateBody        map[string]any
	rawBodyOverride     string // set when a scenario needs literally-malformed JSON
	noBody              bool
	requiredFieldNames  []string
	lastFieldChecked    string
	dispatchCalled      bool

	// ---- multi-trigger-dispatch.feature ----
	triggerIDs         []string          // workflow IDs, in trigger order
	triggerOutcome     map[string]string // workflow ID -> "success" | "fail"
	strategy           spec.ReturnStrategy
	dispatched         map[string]bool
	dispatchedMu       sync.Mutex
	batchStatus        string
	batchResults       []map[string]any
	firstRunStatusCode int // stashed by "returnStrategy has no effect" style checks

	// slowGate/slowID/fastID implement the concurrency proof for "Triggers
	// are dispatched concurrently, not sequentially": the trigger named
	// slowID blocks on slowGate until the trigger named fastID (dispatched
	// on another goroutine - see internal/gateway's dispatchAll) closes it.
	// If dispatch ever regressed to sequential, fastID's Dispatch call
	// would never happen while slowID is still blocking it, and the whole
	// thing times out instead of silently passing.
	slowGate       chan struct{}
	slowID, fastID string
}

// reset returns w to a blank slate before each scenario.
func (w *world) reset() {
	*w = world{
		pathParams:     map[string]string{},
		bodyFields:     map[string]any{},
		queryParams:    map[string][]string{},
		headers:        map[string][]string{},
		schema:         map[string]any{},
		validateBody:   map[string]any{},
		triggerOutcome: map[string]string{},
		dispatched:     map[string]bool{},
		strategy:       spec.ReturnStrategyAcceptPartial,
	}
}

// wasDispatched reports whether workflowID was ever passed to
// multiDispatcher.Dispatch, guarded by dispatchedMu since dispatchAll
// invokes it from more than one goroutine concurrently (see
// internal/gateway/dispatch_handler.go).
func (w *world) wasDispatched(workflowID string) bool {
	w.dispatchedMu.Lock()
	defer w.dispatchedMu.Unlock()
	return w.dispatched[workflowID]
}

// --- shared fakes (gateway.Dispatcher implementations) ---

// echoDispatcher always succeeds and reports workflowID back verbatim in a
// response.WorkflowStarted, the same success shape
// internal/temporal.startWorkflow produces. Used wherever a scenario is
// really testing the gateway's own routing/templating machinery and
// doesn't care what Temporal itself would have done.
type echoDispatcher struct{}

func (echoDispatcher) Dispatch(_ context.Context, _ spec.TemporalBinding, workflowID string, _ any) (any, error) {
	return response.WorkflowStarted{
		Envelope:   response.Envelope{Status: response.StatusStarted},
		WorkflowID: workflowID,
	}, nil
}

// recordingDispatcher is echoDispatcher plus a called flag, for scenarios
// that need to assert whether Temporal dispatch was ever reached (e.g. a
// validation failure must never reach it).
type recordingDispatcher struct {
	called bool
}

func (d *recordingDispatcher) Dispatch(ctx context.Context, binding spec.TemporalBinding, workflowID string, body any) (any, error) {
	d.called = true
	return echoDispatcher{}.Dispatch(ctx, binding, workflowID, body)
}

// --- shared HTTP plumbing ---

// buildHandler is gateway.NewHandler, named to read well at each call
// site; apiSpec's Paths map is the only thing that varies per scenario.
func buildHandler(apiSpec *spec.Spec, dispatcher gateway.Dispatcher) http.Handler {
	return gateway.NewHandler(apiSpec, dispatcher, testLogger)
}

// fireRequest sends method/path (path already has any query string
// appended) through handler, with the given headers and JSON-encodable
// body (nil for no body at all), and returns the recorder plus the
// generically-decoded JSON response body (nil if the body wasn't a JSON
// object, e.g. a bare array - none of these scenarios produce that).
func fireRequest(handler http.Handler, method, path string, headers map[string][]string, body any) (*httptest.ResponseRecorder, map[string]any) {
	var reader *bytes.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		reader = bytes.NewReader(raw)
	} else {
		reader = bytes.NewReader(nil)
	}

	req := httptest.NewRequest(method, path, reader)
	for name, values := range headers {
		for _, v := range values {
			req.Header.Add(name, v)
		}
	}

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	var decoded map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &decoded) // best-effort; nil on failure
	return rec, decoded
}

// fireRequestRaw is fireRequest for a scenario that needs to send literal,
// possibly-malformed bytes as the body rather than a Go value to encode -
// e.g. request-validation.feature's "the request body is not valid JSON".
func fireRequestRaw(handler http.Handler, method, path string, rawBody string) (*httptest.ResponseRecorder, map[string]any) {
	req := httptest.NewRequest(method, path, bytes.NewReader([]byte(rawBody)))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	var decoded map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &decoded)
	return rec, decoded
}
