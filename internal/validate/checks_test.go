package validate

import "testing"

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
