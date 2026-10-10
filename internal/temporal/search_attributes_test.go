package temporal

import (
	"fmt"
	"testing"
	"time"

	sdktemporal "go.temporal.io/sdk/temporal"

	"temporal-gateway/internal/spec"
)

func TestBuildTypedSearchAttributes(t *testing.T) {
	attrs := []spec.SearchAttribute{
		{Name: "CustomStringField", Type: "string", Value: "widget"},
		{Name: "CustomKeywordField", Type: "keyword", Value: "gold"},
		{Name: "CustomBoolField", Type: "bool", Value: true},
		{Name: "CustomIntField", Type: "int", Value: 42},
		{Name: "CustomFloatField", Type: "float", Value: 3.5},
		{Name: "CustomTimeField", Type: "time", Value: "2026-01-02T15:04:05Z"},
		{Name: "CustomKeywordListField", Type: "keywordList", Value: []any{"a", "b"}},
	}

	sa, err := buildTypedSearchAttributes(attrs)
	if err != nil {
		t.Fatalf("buildTypedSearchAttributes returned error: %v", err)
	}
	if sa.Size() != len(attrs) {
		t.Fatalf("Size() = %d, want %d", sa.Size(), len(attrs))
	}

	if v, ok := sa.GetString(sdktemporal.NewSearchAttributeKeyString("CustomStringField")); !ok || v != "widget" {
		t.Errorf("CustomStringField = (%q, %v), want (\"widget\", true)", v, ok)
	}
	if v, ok := sa.GetKeyword(sdktemporal.NewSearchAttributeKeyKeyword("CustomKeywordField")); !ok || v != "gold" {
		t.Errorf("CustomKeywordField = (%q, %v), want (\"gold\", true)", v, ok)
	}
	if v, ok := sa.GetBool(sdktemporal.NewSearchAttributeKeyBool("CustomBoolField")); !ok || v != true {
		t.Errorf("CustomBoolField = (%v, %v), want (true, true)", v, ok)
	}
	if v, ok := sa.GetInt64(sdktemporal.NewSearchAttributeKeyInt64("CustomIntField")); !ok || v != 42 {
		t.Errorf("CustomIntField = (%d, %v), want (42, true)", v, ok)
	}
	if v, ok := sa.GetFloat64(sdktemporal.NewSearchAttributeKeyFloat64("CustomFloatField")); !ok || v != 3.5 {
		t.Errorf("CustomFloatField = (%v, %v), want (3.5, true)", v, ok)
	}
	wantTime, _ := time.Parse(time.RFC3339, "2026-01-02T15:04:05Z")
	if v, ok := sa.GetTime(sdktemporal.NewSearchAttributeKeyTime("CustomTimeField")); !ok || !v.Equal(wantTime) {
		t.Errorf("CustomTimeField = (%v, %v), want (%v, true)", v, ok, wantTime)
	}
	if v, ok := sa.GetKeywordList(sdktemporal.NewSearchAttributeKeyKeywordList("CustomKeywordListField")); !ok || len(v) != 2 || v[0] != "a" || v[1] != "b" {
		t.Errorf("CustomKeywordListField = (%v, %v), want ([a b], true)", v, ok)
	}
}

func TestSearchAttributeUpdateRejectsTypeMismatch(t *testing.T) {
	// spec.validate is expected to catch these before they ever reach here,
	// but searchAttributeUpdate must fail safe (not panic, not silently
	// coerce) if it's ever handed a mismatched value anyway.
	tests := []spec.SearchAttribute{
		{Name: "n", Type: "string", Value: 1},
		{Name: "n", Type: "bool", Value: "true"},
		{Name: "n", Type: "int", Value: "1"},
		{Name: "n", Type: "float", Value: "1.5"},
		{Name: "n", Type: "time", Value: "not-a-timestamp"},
		{Name: "n", Type: "keywordList", Value: "not-a-list"},
		{Name: "n", Type: "keyword", Value: 1},
		{Name: "n", Type: "time", Value: 1},
		{Name: "n", Type: "keywordList", Value: []any{"a", 1}},
		{Name: "n", Type: "bogus", Value: "x"},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%s/%v", tt.Type, tt.Value), func(t *testing.T) {
			if _, err := searchAttributeUpdate(tt); err == nil {
				t.Errorf("searchAttributeUpdate(%+v) expected an error, got nil", tt)
			}
		})
	}
}
