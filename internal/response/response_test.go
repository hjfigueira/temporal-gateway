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
		StatusInvalidRequest, StatusValidationFailed, StatusFailed,
	}
	for _, s := range errorStatuses {
		if !s.IsError() {
			t.Errorf("%s.IsError() = false, want true", s)
		}
	}

	successStatuses := []Status{StatusValid, StatusStarted, StatusSignaled, StatusCancelled, StatusTerminated}
	for _, s := range successStatuses {
		if s.IsError() {
			t.Errorf("%s.IsError() = true, want false", s)
		}
	}

	// A Status value IsError doesn't recognize at all (e.g. a new constant
	// added elsewhere without updating IsError's success list) must read as
	// an error, not silently pass as success - see IsError's doc comment.
	if unknown := Status("SOMETHING_NEW"); !unknown.IsError() {
		t.Errorf("%s.IsError() = false, want true (unrecognized statuses must fail closed)", unknown)
	}
}
