package validate

import "testing"

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
