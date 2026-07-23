package response

import (
	"encoding/json"
	"testing"
)

func TestEnvelopeMarshalsFlat(t *testing.T) {
	data, err := json.Marshal(Envelope{Success: true})
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	if got, want := string(data), `{"success":true}`; got != want {
		t.Errorf("Marshal(Envelope{Success:true}) = %s, want %s", got, want)
	}

	data, err = json.Marshal(Envelope{Success: false, Message: "boom"})
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	if got, want := string(data), `{"success":false,"message":"boom"}`; got != want {
		t.Errorf("Marshal(Envelope{...}) = %s, want %s", got, want)
	}
}

func TestWorkflowStartedEmbedsEnvelopeFlat(t *testing.T) {
	ws := WorkflowStarted{
		Envelope:   Envelope{Success: true, Message: "workflow started"},
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
		"success":    true,
		"message":    "workflow started",
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
