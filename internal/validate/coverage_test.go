package validate

import (
	"reflect"
	"testing"
)

func TestSchemaEdgeCases(t *testing.T) {
	schema := map[string]any{
		"type":     "object",
		"required": []any{"customer", 42}, // a non-string entry is ignored
		"properties": map[string]any{
			"customer": map[string]any{
				"type":       "object",
				"properties": map[string]any{"city": map[string]any{"type": "string"}},
			},
			"nothing":  map[string]any{"type": "null"},
			"anything": map[string]any{"type": "made-up-type"},
			"loose":    "not a schema map",              // ignored
			"tags":     map[string]any{"type": "array"}, // no items schema
			"level":    map[string]any{"enum": []any{1, int64(2), float32(3)}},
		},
	}
	data := map[string]any{
		"customer": map[string]any{"city": 7.0},
		"nothing":  "x",
		"anything": 1.0,
		"loose":    true,
		"tags":     []any{1.0, "two"},
		"level":    "1",
	}

	want := map[string][]string{
		"customer.city": {msgType("customer.city", "string")},
		"nothing":       {msgType("nothing", "null")},
		"level":         {msgEnum("level")},
	}
	if got := Schema(schema, data).Fields; !reflect.DeepEqual(got, want) {
		t.Fatalf("Fields = %v, want %v", got, want)
	}

	for _, ok := range []any{1.0, 2.0, 3.0} {
		data["level"] = ok
		data["customer"] = map[string]any{"city": "x"}
		data["nothing"] = nil
		if r := Schema(schema, data); len(r.Fields) != 0 {
			t.Fatalf("level=%v: unexpected violations %v", ok, r.Fields)
		}
	}
}

func TestMsgTypeCoversEveryType(t *testing.T) {
	for _, want := range []string{"string", "number", "integer", "boolean", "array", "object", "null", "other"} {
		if msgType("f", want) == "" {
			t.Errorf("msgType(%q) is empty", want)
		}
	}
}

func TestToFloat(t *testing.T) {
	for _, v := range []any{float64(1), float32(1), int(1), int64(1)} {
		if f, ok := toFloat(v); !ok || f != 1 {
			t.Errorf("toFloat(%T) = %v, %v", v, f, ok)
		}
	}
	if _, ok := toFloat("1"); ok {
		t.Error(`toFloat("1") ok = true, want false`)
	}
}
