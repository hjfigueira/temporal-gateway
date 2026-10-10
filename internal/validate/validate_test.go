package validate

import (
	"encoding/json"
	"strings"
	"testing"

	"temporal-gateway/internal/response"
)

// decode parses a JSON literal the way the gateway does, so numbers come
// out as json.Number and objects as map[string]any.
func decode(t *testing.T, jsonLiteral string) any {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(jsonLiteral))
	dec.UseNumber()
	var data any
	if err := dec.Decode(&data); err != nil {
		t.Fatalf("invalid test JSON %q: %v", jsonLiteral, err)
	}
	return data
}

func TestSchemaOrderPayload(t *testing.T) {
	schema := map[string]any{
		"type":     "object",
		"required": []any{"orderId", "customerId"},
		"properties": map[string]any{
			"orderId":    map[string]any{"type": "string"},
			"customerId": map[string]any{"type": "string"},
			"items": map[string]any{
				"type":  "array",
				"items": map[string]any{"type": "string"},
			},
		},
	}

	t.Run("valid payload passes", func(t *testing.T) {
		data := decode(t, `{"orderId":"o1","customerId":"c1","items":["a","b"]}`)
		result := Schema(schema, data)
		if result.Status.IsError() {
			t.Fatalf("expected success, got %+v", result)
		}
		if result.Status != response.StatusValid {
			t.Errorf("Status = %q, want %q", result.Status, response.StatusValid)
		}
		if result.Fields != nil {
			t.Errorf("expected no fields on success, got %+v", result.Fields)
		}
	})

	t.Run("missing required field fails", func(t *testing.T) {
		data := decode(t, `{"customerId":"c1"}`)
		result := Schema(schema, data)
		if !result.Status.IsError() {
			t.Fatal("expected failure for missing orderId")
		}
		if result.Status != response.StatusValidationFailed {
			t.Errorf("Status = %q, want %q", result.Status, response.StatusValidationFailed)
		}
		if result.Message == "" {
			t.Error("expected a human-readable message")
		}
		wantRule := "The orderId field is required."
		rules, ok := result.Fields["orderId"]
		if !ok || len(rules) != 1 || rules[0] != wantRule {
			t.Errorf("Fields[\"orderId\"] = %v, want [%q]", rules, wantRule)
		}
	})

	t.Run("wrong property type fails", func(t *testing.T) {
		data := decode(t, `{"orderId":123,"customerId":"c1"}`)
		result := Schema(schema, data)
		if !result.Status.IsError() {
			t.Fatal("expected failure for orderId with wrong type")
		}
		if _, ok := result.Fields["orderId"]; !ok {
			t.Errorf("expected a violation under \"orderId\", got Fields=%+v", result.Fields)
		}
	})

	t.Run("wrong array item type fails under an indexed field path", func(t *testing.T) {
		data := decode(t, `{"orderId":"o1","customerId":"c1","items":["a",2]}`)
		result := Schema(schema, data)
		if !result.Status.IsError() {
			t.Fatal("expected failure for a non-string item")
		}
		if _, ok := result.Fields["items[1]"]; !ok {
			t.Errorf("expected a violation under \"items[1]\", got Fields=%+v", result.Fields)
		}
	})

	t.Run("top-level type mismatch fails under the root field", func(t *testing.T) {
		data := decode(t, `["not","an","object"]`)
		result := Schema(schema, data)
		if !result.Status.IsError() {
			t.Fatal("expected failure for an array where an object is required")
		}
		if _, ok := result.Fields[rootField]; !ok {
			t.Errorf("expected a violation under %q, got Fields=%+v", rootField, result.Fields)
		}
	})

	t.Run("multiple independent violations are all reported", func(t *testing.T) {
		// orderId is missing entirely, customerId has the wrong type: two
		// unrelated fields should both surface, not just the first found.
		data := decode(t, `{"customerId":123}`)
		result := Schema(schema, data)
		if !result.Status.IsError() {
			t.Fatal("expected failure")
		}
		if _, ok := result.Fields["orderId"]; !ok {
			t.Errorf("expected a violation under \"orderId\", got Fields=%+v", result.Fields)
		}
		if _, ok := result.Fields["customerId"]; !ok {
			t.Errorf("expected a violation under \"customerId\", got Fields=%+v", result.Fields)
		}
	})
}
