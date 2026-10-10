package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestRenderTemplateSources(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/orders/o1?region=eu", strings.NewReader(""))
	req.Header.Set("X-Request-Id", "req-123")

	pathParams := map[string]string{"orderId": "o1"}
	body := map[string]any{"customerId": "c1", "priority": float64(2)}

	resolve := fieldResolver(req, pathParams, body)

	tests := []struct {
		name       string
		tmpl       string
		want       string
		unresolved bool
	}{
		{"path origin", "order-{path.orderId}", "order-o1", false},
		{"body origin string", "customer-{body.customerId}", "customer-c1", false},
		{"body origin number", "priority-{body.priority}", "priority-2", false},
		{"query origin", "region-{query.region}", "region-eu", false},
		{"header origin", "trace-{header.X-Request-Id}", "trace-req-123", false},
		{"header origin lowercase", "trace-{header.x-request-id}", "trace-req-123", false},
		{"unresolved field reported", "unknown-{path.missing}", "unknown-{path.missing}", true},
		{"unknown origin reported", "unknown-{env.missing}", "unknown-{env.missing}", true},
		{"missing origin reported", "bare-{orderId}", "bare-{orderId}", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, unresolved := renderTemplate(tt.tmpl, resolve)
			if got != tt.want {
				t.Errorf("renderTemplate(%q) = %q, want %q", tt.tmpl, got, tt.want)
			}
			if (len(unresolved) > 0) != tt.unresolved {
				t.Errorf("renderTemplate(%q) unresolved = %v, want unresolved=%v", tt.tmpl, unresolved, tt.unresolved)
			}
		})
	}
}

func TestRenderTemplateUUIDv7(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/orders", strings.NewReader(""))
	resolve := fieldResolver(req, nil, nil)

	got, _ := renderTemplate("order-{uuidv7}", resolve)
	id := strings.TrimPrefix(got, "order-")

	parsed, err := uuid.Parse(id)
	if err != nil {
		t.Fatalf("generated id %q is not a valid uuid: %v", id, err)
	}
	if parsed.Version() != 7 {
		t.Errorf("generated id %q has version %d, want 7", id, parsed.Version())
	}

	// Case-insensitive keyword.
	if got3, _ := renderTemplate("{UUIDv7}", resolve); got3 == "{UUIDv7}" {
		t.Errorf("UUIDv7 keyword should be case-insensitive, got %q", got3)
	}

	// A fresh value is generated per occurrence.
	first, _ := renderTemplate("{uuidv7}", resolve)
	second, _ := renderTemplate("{uuidv7}", resolve)
	if first == second {
		t.Errorf("expected distinct uuids per render, got %q twice", first)
	}
}
