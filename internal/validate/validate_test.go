package validate

import (
	"encoding/json"
	"testing"

	"temporal-gateway/internal/response"
)

// decode parses a JSON literal the way the gateway does, so numbers come
// out as float64 and objects as map[string]any.
func decode(t *testing.T, jsonLiteral string) any {
	t.Helper()
	var data any
	if err := json.Unmarshal([]byte(jsonLiteral), &data); err != nil {
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

func TestMessagesFollowLaravelWording(t *testing.T) {
	tests := []struct {
		name   string
		schema map[string]any
		data   any
		field  string
		want   string
	}{
		{
			name:   "required",
			schema: map[string]any{"type": "object", "required": []any{"name"}},
			data:   decode(t, `{}`),
			field:  "name",
			want:   "The name field is required.",
		},
		{
			name:   "string type",
			schema: map[string]any{"type": "object", "properties": map[string]any{"name": map[string]any{"type": "string"}}},
			data:   decode(t, `{"name":1}`),
			field:  "name",
			want:   "The name must be a string.",
		},
		{
			name:   "integer type",
			schema: map[string]any{"type": "object", "properties": map[string]any{"age": map[string]any{"type": "integer"}}},
			data:   decode(t, `{"age":"old"}`),
			field:  "age",
			want:   "The age must be an integer.",
		},
		{
			name:   "boolean type",
			schema: map[string]any{"type": "object", "properties": map[string]any{"active": map[string]any{"type": "boolean"}}},
			data:   decode(t, `{"active":"yes"}`),
			field:  "active",
			want:   "The active field must be true or false.",
		},
		{
			name:   "minLength",
			schema: map[string]any{"type": "object", "properties": map[string]any{"code": map[string]any{"type": "string", "minLength": 5}}},
			data:   decode(t, `{"code":"ab"}`),
			field:  "code",
			want:   "The code must be at least 5 characters.",
		},
		{
			name:   "maxLength",
			schema: map[string]any{"type": "object", "properties": map[string]any{"code": map[string]any{"type": "string", "maxLength": 2}}},
			data:   decode(t, `{"code":"abc"}`),
			field:  "code",
			want:   "The code must not be greater than 2 characters.",
		},
		{
			name:   "pattern",
			schema: map[string]any{"type": "object", "properties": map[string]any{"code": map[string]any{"type": "string", "pattern": "^[0-9]+$"}}},
			data:   decode(t, `{"code":"abc"}`),
			field:  "code",
			want:   "The code format is invalid.",
		},
		{
			name:   "minimum",
			schema: map[string]any{"type": "object", "properties": map[string]any{"age": map[string]any{"type": "number", "minimum": 18}}},
			data:   decode(t, `{"age":10}`),
			field:  "age",
			want:   "The age must be at least 18.",
		},
		{
			name:   "maximum",
			schema: map[string]any{"type": "object", "properties": map[string]any{"age": map[string]any{"type": "number", "maximum": 65}}},
			data:   decode(t, `{"age":70}`),
			field:  "age",
			want:   "The age must not be greater than 65.",
		},
		{
			name:   "enum",
			schema: map[string]any{"type": "object", "properties": map[string]any{"status": map[string]any{"enum": []any{"open", "closed"}}}},
			data:   decode(t, `{"status":"pending"}`),
			field:  "status",
			want:   "The selected status is invalid.",
		},
		{
			name: "additionalProperties prohibited",
			schema: map[string]any{
				"type":                 "object",
				"properties":           map[string]any{"a": map[string]any{"type": "string"}},
				"additionalProperties": false,
			},
			data:  decode(t, `{"a":"x","extra":"y"}`),
			field: "extra",
			want:  "The extra field is prohibited.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := Schema(tt.schema, tt.data)
			if !result.Status.IsError() {
				t.Fatal("expected failure")
			}
			rules, ok := result.Fields[tt.field]
			if !ok || len(rules) != 1 || rules[0] != tt.want {
				t.Errorf("Fields[%q] = %v, want [%q]", tt.field, rules, tt.want)
			}
		})
	}
}

func TestSchemaAccumulatesMultipleRulesOnSameField(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"code": map[string]any{
				"type":      "string",
				"minLength": 5,
				"pattern":   "^[0-9]+$",
			},
		},
	}
	data := decode(t, `{"code":"ab"}`)

	result := Schema(schema, data)
	if !result.Status.IsError() {
		t.Fatal("expected failure")
	}

	rules := result.Fields["code"]
	if len(rules) != 2 {
		t.Fatalf("Fields[\"code\"] = %v, want 2 rules (minLength and pattern) both reported", rules)
	}
}

func TestSchemaConstraints(t *testing.T) {
	tests := []struct {
		name    string
		schema  map[string]any
		data    any
		wantErr bool
	}{
		{
			name:    "enum accepts member",
			schema:  map[string]any{"enum": []any{"a", "b"}},
			data:    decode(t, `"a"`),
			wantErr: false,
		},
		{
			name:    "enum rejects non-member",
			schema:  map[string]any{"enum": []any{"a", "b"}},
			data:    decode(t, `"c"`),
			wantErr: true,
		},
		{
			name:    "minLength rejects short string",
			schema:  map[string]any{"type": "string", "minLength": 3},
			data:    decode(t, `"ab"`),
			wantErr: true,
		},
		{
			name:    "maxLength rejects long string",
			schema:  map[string]any{"type": "string", "maxLength": 2},
			data:    decode(t, `"abc"`),
			wantErr: true,
		},
		{
			name:    "pattern rejects mismatch",
			schema:  map[string]any{"type": "string", "pattern": "^[0-9]+$"},
			data:    decode(t, `"abc"`),
			wantErr: true,
		},
		{
			name:    "pattern accepts match",
			schema:  map[string]any{"type": "string", "pattern": "^[0-9]+$"},
			data:    decode(t, `"123"`),
			wantErr: false,
		},
		{
			name:    "minimum rejects below range",
			schema:  map[string]any{"type": "number", "minimum": 10},
			data:    decode(t, `5`),
			wantErr: true,
		},
		{
			name:    "maximum rejects above range",
			schema:  map[string]any{"type": "number", "maximum": 10},
			data:    decode(t, `15`),
			wantErr: true,
		},
		{
			name:    "integer rejects fractional number",
			schema:  map[string]any{"type": "integer"},
			data:    decode(t, `1.5`),
			wantErr: true,
		},
		{
			name:    "integer accepts whole number",
			schema:  map[string]any{"type": "integer"},
			data:    decode(t, `2`),
			wantErr: false,
		},
		{
			name: "additionalProperties false rejects unknown field",
			schema: map[string]any{
				"type":                 "object",
				"properties":           map[string]any{"a": map[string]any{"type": "string"}},
				"additionalProperties": false,
			},
			data:    decode(t, `{"a":"x","b":"y"}`),
			wantErr: true,
		},
		{
			name: "additionalProperties unset allows unknown field",
			schema: map[string]any{
				"type":       "object",
				"properties": map[string]any{"a": map[string]any{"type": "string"}},
			},
			data:    decode(t, `{"a":"x","b":"y"}`),
			wantErr: false,
		},
		{
			name:    "empty schema always passes",
			schema:  nil,
			data:    decode(t, `{"anything":"goes"}`),
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := Schema(tt.schema, tt.data)
			if result.Status.IsError() != tt.wantErr {
				t.Errorf("Schema() IsError() = %v, wantErr %v (result: %+v)", result.Status.IsError(), tt.wantErr, result)
			}
		})
	}
}
