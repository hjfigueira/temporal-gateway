package validate

import (
	"strings"
	"testing"
)

func TestCheckSchema(t *testing.T) {
	good := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"sku":  map[string]any{"type": "string", "pattern": "^[A-Z]+$"},
			"tags": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		},
	}
	if err := CheckSchema(good); err != nil {
		t.Fatalf("CheckSchema(valid) = %v", err)
	}
	if err := CheckSchema(nil); err != nil {
		t.Fatalf("CheckSchema(nil) = %v", err)
	}

	bad := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"sku":  map[string]any{"type": "strnig"},
			"tags": map[string]any{"type": "array", "items": map[string]any{"pattern": "(["}},
		},
	}
	err := CheckSchema(bad)
	if err == nil {
		t.Fatal("CheckSchema(bad) = nil, want errors")
	}
	for _, want := range []string{`sku: unknown type "strnig"`, "tags[]: invalid pattern"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}
