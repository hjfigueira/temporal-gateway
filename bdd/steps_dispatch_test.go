package bdd

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/cucumber/godog"

	"temporal-gateway/internal/response"
	"temporal-gateway/internal/spec"
)

// registerDispatchSteps wires up
// .specs/features/multi-trigger-dispatch.feature. It drives real,
// concurrent dispatchAll fan-out (internal/gateway/dispatch_handler.go)
// through gateway.NewHandler with a configurable fake Dispatcher, so the
// returnStrategy/207/409 matrix and the "failures don't block siblings"
// and "triggers really run concurrently" claims are checked against the
// gateway's real behavior, not re-derived from reading the code.
func registerDispatchSteps(sc *godog.ScenarioContext, w *world) {
	sc.Given(`^an operation whose x-temporal\.triggers has more than one entry$`, func() error {
		setupTriggers(w, 2)
		return nil
	})
	sc.Given(`^each trigger has its own namespace and workflowId$`, func() error { return nil })

	sc.Given(`^an operation with exactly one trigger$`, func() error {
		w.triggerIDs = w.triggerIDs[:1]
		return nil
	})
	sc.When(`^the trigger succeeds$`, func() error {
		if err := performMultiDispatch(w); err != nil {
			return err
		}
		w.firstRunStatusCode = w.rec.Code
		return nil
	})
	sc.Then(`^the HTTP response is that trigger's own result, not a BatchResult$`, func() error {
		if _, ok := w.decoded["results"]; ok {
			return fmt.Errorf("response included a \"results\" array, want the trigger's own result directly: %v", w.decoded)
		}
		if _, ok := w.decoded["workflowId"]; !ok {
			return fmt.Errorf("response %v doesn't look like a direct WorkflowStarted result", w.decoded)
		}
		return nil
	})
	sc.Then(`^returnStrategy has no effect on a single-trigger operation$`, func() error {
		w.strategy = spec.ReturnStrategyAllOrNothing
		if err := performMultiDispatch(w); err != nil {
			return err
		}
		if w.rec.Code != w.firstRunStatusCode {
			return fmt.Errorf("HTTP status changed from %d to %d just by changing returnStrategy on a single-trigger operation", w.firstRunStatusCode, w.rec.Code)
		}
		return nil
	})

	sc.Given(`^2 triggers on one operation$`, func() error {
		setupTriggers(w, 2)
		return nil
	})
	sc.When(`^both triggers are dispatched and both succeed$`, func() error { return performMultiDispatch(w) })
	sc.Then(`^the top-level status is "([^"]+)"$`, func(want string) error {
		if w.batchStatus != want {
			return fmt.Errorf("top-level status = %q, want %q", w.batchStatus, want)
		}
		return nil
	})
	sc.Then(`^the HTTP status matches the first trigger's own success status$`, func() error {
		if w.rec.Code != http.StatusAccepted {
			return fmt.Errorf("HTTP status = %d, want %d (startWorkflow's own success status)", w.rec.Code, http.StatusAccepted)
		}
		return nil
	})
	sc.Then(`^this is true regardless of returnStrategy$`, func() error {
		w.strategy = spec.ReturnStrategyAllOrNothing
		if err := performMultiDispatch(w); err != nil {
			return err
		}
		if w.rec.Code != http.StatusAccepted || w.batchStatus != string(response.StatusWorkflowStarted) {
			return fmt.Errorf("switching to allOrNothing changed a uniform success's outcome: http=%d status=%s", w.rec.Code, w.batchStatus)
		}
		return nil
	})

	sc.Given(`^2 triggers on one operation, one of which is slow to respond$`, func() error {
		setupTriggers(w, 2)
		w.slowID, w.fastID = w.triggerIDs[0], w.triggerIDs[1]
		w.slowGate = make(chan struct{})
		return nil
	})
	sc.When(`^the operation is dispatched$`, func() error { return performMultiDispatch(w) })
	sc.Then(`^both triggers are started before either one's result is known$`, func() error {
		if len(w.batchResults) != 2 {
			return fmt.Errorf("expected 2 results, got %d: %v", len(w.batchResults), w.batchResults)
		}
		for i, r := range w.batchResults {
			if r["status"] != string(response.StatusStarted) {
				return fmt.Errorf("results[%d].status = %v, want %q - a timeout/failure here means the two triggers were not actually dispatched concurrently", i, r["status"], response.StatusStarted)
			}
		}
		return nil
	})
	sc.Then(`^a slow trigger does not delay the other trigger from starting$`, func() error {
		if !w.wasDispatched(w.fastID) {
			return fmt.Errorf("expected the fast trigger %q to have been dispatched", w.fastID)
		}
		return nil
	})

	sc.Given(`^2 triggers, where the first trigger's Temporal call will fail$`, func() error {
		setupTriggers(w, 2)
		w.triggerOutcome[w.triggerIDs[0]] = "fail"
		return nil
	})
	sc.Then(`^the second trigger is still dispatched and can still succeed$`, func() error {
		second := w.triggerIDs[1]
		if !w.wasDispatched(second) {
			return fmt.Errorf("expected the second trigger %q to have been dispatched", second)
		}
		if len(w.batchResults) != 2 || w.batchResults[1]["status"] != string(response.StatusStarted) {
			return fmt.Errorf("results[1] = %v, want status %q", w.batchResults, response.StatusStarted)
		}
		return nil
	})
	sc.Then(`^the failure of the first is reported in its own result item only$`, func() error {
		if len(w.batchResults) != 2 {
			return fmt.Errorf("expected 2 results, got %d: %v", len(w.batchResults), w.batchResults)
		}
		if w.batchResults[0]["status"] == string(response.StatusStarted) {
			return fmt.Errorf("results[0].status = %v, want a failure status", w.batchResults[0]["status"])
		}
		if w.batchResults[1]["status"] != string(response.StatusStarted) {
			return fmt.Errorf("results[1].status = %v, want %q - the first trigger's failure must not spread to its sibling", w.batchResults[1]["status"], response.StatusStarted)
		}
		return nil
	})

	sc.Given(`^returnStrategy is "acceptPartial" \(or unset\)$`, func() error {
		w.strategy = spec.ReturnStrategyAcceptPartial
		return nil
	})
	sc.Given(`^returnStrategy is "allOrNothing"$`, func() error {
		w.strategy = spec.ReturnStrategyAllOrNothing
		return nil
	})
	sc.Given(`^(\d+) of (\d+) triggers succeed$`, func(succeeded, total int) error {
		setupTriggers(w, total)
		for i, id := range w.triggerIDs {
			if i >= succeeded {
				w.triggerOutcome[id] = "fail"
			}
		}
		return performMultiDispatch(w)
	})
	sc.Then(`^the HTTP status is (\d+)$`, func(code int) error {
		if w.rec.Code != code {
			return fmt.Errorf("HTTP status = %d, want %d", w.rec.Code, code)
		}
		return nil
	})

	sc.Given(`^a multi-trigger operation under either returnStrategy$`, func() error {
		setupTriggers(w, 2)
		return nil
	})
	sc.Then(`^the response's "results" array has one item per trigger$`, func() error {
		if len(w.batchResults) != len(w.triggerIDs) {
			return fmt.Errorf("results has %d items, want %d (one per trigger)", len(w.batchResults), len(w.triggerIDs))
		}
		return nil
	})
	sc.Then(`^each item identifies which workflow/action it is about$`, func() error {
		for i, item := range w.batchResults {
			_, hasID := item["workflowId"]
			msg, _ := item["message"].(string)
			if !hasID && msg == "" {
				return fmt.Errorf("results[%d] = %v identifies neither a workflowId nor names the workflow in its message", i, item)
			}
		}
		return nil
	})

	sc.Given(`^x-temporal\.returnStrategy is set to a value other than "acceptPartial" or "allOrNothing" \(and not empty\)$`, func() error {
		w.rawSpec = `openapi: 3.0.3
info:
  title: Test
  version: "1.0.0"
paths:
  /multi-test:
    post:
      operationId: multiTest
      x-temporal:
        returnStrategy: bogus
        triggers:
          - action: startWorkflow
            namespace: default
            workflowType: TestWorkflow
            workflowId: "wf-1"
            taskQueue: test-task-queue
`
		return nil
	})
	// "the spec is loaded" / "spec loading fails before the server starts"
	// are registered once in steps_spec_loading_test.go and reused here.
}

// setupTriggers (re)builds w's n-trigger default state: workflow IDs
// "wf-1".."wf-n", all set to succeed, with no slow-trigger gating. It
// deliberately leaves w.strategy untouched, since a scenario's "Given
// returnStrategy is ..." step commonly runs before this one.
func setupTriggers(w *world, n int) {
	w.triggerIDs = make([]string, n)
	w.triggerOutcome = map[string]string{}
	w.dispatched = map[string]bool{}
	w.slowGate = nil
	w.slowID, w.fastID = "", ""
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("wf-%d", i+1)
		w.triggerIDs[i] = id
		w.triggerOutcome[id] = "success"
	}
}

// multiDispatcher is the gateway.Dispatcher fake behind every scenario in
// this file: each workflow ID's outcome ("success" or "fail") is looked up
// in w.triggerOutcome, and the slowID/fastID gate (see world.slowGate's
// doc comment) proves genuine concurrency when set.
type multiDispatcher struct{ w *world }

func (d multiDispatcher) Dispatch(_ context.Context, _ spec.TemporalBinding, workflowID string, _ any) (any, error) {
	d.w.dispatchedMu.Lock()
	d.w.dispatched[workflowID] = true
	d.w.dispatchedMu.Unlock()

	if d.w.slowGate != nil {
		switch workflowID {
		case d.w.slowID:
			select {
			case <-d.w.slowGate:
			case <-time.After(2 * time.Second):
				return nil, fmt.Errorf("timed out waiting for the other trigger to start - dispatch is not happening concurrently")
			}
		case d.w.fastID:
			close(d.w.slowGate)
		}
	}

	if d.w.triggerOutcome[workflowID] == "fail" {
		return nil, fmt.Errorf("dispatch failed for %s", workflowID)
	}
	return response.WorkflowStarted{
		Envelope:   response.Envelope{Status: response.StatusStarted},
		WorkflowID: workflowID,
	}, nil
}

// performMultiDispatch builds a single operation whose x-temporal.triggers
// is exactly w.triggerIDs (in order), registers it with a multiDispatcher,
// fires one POST through it, and stashes the HTTP status, the generically
// decoded response, and (when present) the batch's top-level status and
// per-trigger results.
func performMultiDispatch(w *world) error {
	triggers := make([]spec.TemporalBinding, len(w.triggerIDs))
	for i, id := range w.triggerIDs {
		triggers[i] = spec.TemporalBinding{
			Action:       spec.ActionStartWorkflow,
			Namespace:    "default",
			WorkflowType: "TestWorkflow",
			TaskQueue:    "test-task-queue",
			WorkflowID:   id,
		}
	}
	op := &spec.Operation{
		OperationID: "multiTest",
		Temporal: spec.TemporalSpec{
			ReturnStrategy: w.strategy,
			Triggers:       triggers,
		},
	}
	apiSpec := &spec.Spec{Paths: map[string]spec.PathItem{"/multi-test": {Post: op}}}
	handler := buildHandler(apiSpec, multiDispatcher{w: w})

	rec, decoded := fireRequest(handler, http.MethodPost, "/multi-test", nil, map[string]any{})
	w.rec, w.decoded, w.lastStatus = rec, decoded, rec.Code

	w.batchStatus, _ = decoded["status"].(string)
	w.batchResults = nil
	if results, ok := decoded["results"].([]any); ok {
		w.batchResults = make([]map[string]any, len(results))
		for i, r := range results {
			m, _ := r.(map[string]any)
			w.batchResults[i] = m
		}
	}
	return nil
}
