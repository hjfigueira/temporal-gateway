package spec

import (
	"reflect"
	"strings"
	"testing"
)

func TestParsePlaceholder(t *testing.T) {
	body := func(path ...PathSegment) Placeholder {
		return Placeholder{Kind: PlaceholderField, Ref: Reference{Origin: "body", Path: path}}
	}
	key := func(k string) PathSegment { return PathSegment{Key: k} }
	idx := func(i int) PathSegment { return PathSegment{Index: i, IsIndex: true} }

	tests := []struct {
		in   string
		want Placeholder
	}{
		{"UUIDv7", Placeholder{Kind: PlaceholderUUIDv7}},
		{"path.orderId", Placeholder{Kind: PlaceholderField, Ref: Reference{Origin: "path", Name: "orderId"}}},
		{"Header.X-Request-Id", Placeholder{Kind: PlaceholderField, Ref: Reference{Origin: "header", Name: "X-Request-Id"}}},
		{"query.a.b", Placeholder{Kind: PlaceholderField, Ref: Reference{Origin: "query", Name: "a.b"}}},
		{"body.customer.id", body(key("customer"), key("id"))},
		{"body.items[2].sku", body(key("items"), idx(2), key("sku"))},
		{"body[0][1]", body(idx(0), idx(1))},
		{"fingerprint(body)", Placeholder{Kind: PlaceholderFingerprint, Ref: Reference{Origin: "body"}}},
		{"Fingerprint(body.items[2])", Placeholder{Kind: PlaceholderFingerprint, Ref: Reference{Origin: "body", Path: []PathSegment{key("items"), idx(2)}}}},
		{"fingerprint(query.q)", Placeholder{Kind: PlaceholderFingerprint, Ref: Reference{Origin: "query", Name: "q"}}},
	}
	for _, tt := range tests {
		got, err := ParsePlaceholder(tt.in)
		if err != nil {
			t.Errorf("ParsePlaceholder(%q): %v", tt.in, err)
			continue
		}
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("ParsePlaceholder(%q) = %+v, want %+v", tt.in, got, tt.want)
		}
	}
}

func TestParsePlaceholderErrors(t *testing.T) {
	tests := map[string]string{
		"body":                  "whole body",
		"fingerprint(body":      "closing parenthesis",
		"fingerprint()":         "must be",
		"fingerprint(cookie.a)": "unknown origin",
		"path":                  "must be",
		"path.":                 "must be",
		".x":                    "must be",
		"orderId":               "must be",
		"paht.id":               `unknown origin "paht"`,
		"body..x":               "empty field name",
		"body.items[2":          "unclosed '['",
		"body.items[x]":         "not a non-negative integer",
		"body.items[-1]":        "not a non-negative integer",
		"body.items[0]x":        "expected '.' or '['",
	}
	for in, wantErr := range tests {
		if _, err := ParsePlaceholder(in); err == nil || !strings.Contains(err.Error(), wantErr) {
			t.Errorf("ParsePlaceholder(%q) err = %v, want it to contain %q", in, err, wantErr)
		}
	}
}
