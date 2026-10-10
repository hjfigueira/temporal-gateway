package response

import (
	"encoding/json"
	"testing"
)

func TestEnvelopeMarshalsFlat(t *testing.T) {
	data, err := json.Marshal(Envelope{Status: StatusStarted})
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	if got, want := string(data), `{"status":"STARTED"}`; got != want {
		t.Errorf("Marshal(Envelope{Status:StatusStarted}) = %s, want %s", got, want)
	}

	data, err = json.Marshal(Envelope{Status: StatusFailed, Message: "boom"})
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	if got, want := string(data), `{"status":"FAILED","message":"boom"}`; got != want {
		t.Errorf("Marshal(Envelope{...}) = %s, want %s", got, want)
	}
}

func TestWorkflowStartedEmbedsEnvelopeFlat(t *testing.T) {
	ws := WorkflowStarted{
		Envelope:   Envelope{Status: StatusStarted},
		WorkflowID: "order-1",
		RunID:      "run-1",
	}

	data, err := json.Marshal(ws)
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal returned error: %v", err)
	}

	// The embedded Envelope's fields must be promoted (flattened) into the
	// same JSON object as WorkflowStarted's own fields, not nested under an
	// "Envelope" key - that's the point of using composition here.
	want := map[string]any{
		"status":     "STARTED",
		"workflowId": "order-1",
		"runId":      "run-1",
	}
	for key, wantVal := range want {
		if got, ok := decoded[key]; !ok || got != wantVal {
			t.Errorf("decoded[%q] = %v, want %v (full: %s)", key, got, wantVal, data)
		}
	}
	if _, ok := decoded["Envelope"]; ok {
		t.Errorf("expected Envelope to be flattened, not nested: %s", data)
	}
}

func TestStatusIsError(t *testing.T) {
	errorStatuses := []Status{
		StatusDuplicated, StatusNotFound, StatusInvalidArgument, StatusForbidden,
		StatusInvalidRequest, StatusValidationFailed, StatusPayloadTooLarge, StatusTimeout, StatusUnavailable, StatusFailed,
		StatusBatchPartiallySucceeded, StatusBatchFailed,
	}
	for _, s := range errorStatuses {
		if !s.IsError() {
			t.Errorf("%s.IsError() = false, want true", s)
		}
	}

	successStatuses := []Status{StatusStarted, StatusSignaled, StatusCancelled, StatusTerminated, StatusBatchSucceeded}
	for _, s := range successStatuses {
		if s.IsError() {
			t.Errorf("%s.IsError() = true, want false", s)
		}
	}
}

func TestOutcome(t *testing.T) {
	started := WorkflowStarted{Envelope: Envelope{Status: StatusStarted}}
	if o := Ack(started, started.Envelope); !o.Succeeded() || o.IsRaw() {
		t.Errorf("Ack(STARTED) = %+v, want a succeeded acknowledgement", o)
	}
	attached := Envelope{Status: StatusWorkflowRunning}
	if o := Ack(attached, attached); o.Succeeded() {
		t.Errorf("Ack(WORKFLOW_RUNNING) succeeded, want not")
	}
	if o := Raw(42); !o.Succeeded() || !o.IsRaw() {
		t.Errorf("Raw(42) = %+v, want a succeeded raw result", o)
	}
}
