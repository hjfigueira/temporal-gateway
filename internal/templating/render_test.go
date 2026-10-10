package templating

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

	resolve := sourceOf(req, pathParams, body).resolve

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
			got, unresolved := render(tt.tmpl, resolve)
			if got != tt.want {
				t.Errorf("render(%q) = %q, want %q", tt.tmpl, got, tt.want)
			}
			if (len(unresolved) > 0) != tt.unresolved {
				t.Errorf("render(%q) unresolved = %v, want unresolved=%v", tt.tmpl, unresolved, tt.unresolved)
			}
		})
	}
}

func TestRenderTemplateUUIDv7(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/orders", strings.NewReader(""))
	resolve := sourceOf(req, nil, nil).resolve

	got, _ := render("order-{uuidv7}", resolve)
	id := strings.TrimPrefix(got, "order-")

	parsed, err := uuid.Parse(id)
	if err != nil {
		t.Fatalf("generated id %q is not a valid uuid: %v", id, err)
	}
	if parsed.Version() != 7 {
		t.Errorf("generated id %q has version %d, want 7", id, parsed.Version())
	}

	// Case-insensitive keyword.
	if got3, _ := render("{UUIDv7}", resolve); got3 == "{UUIDv7}" {
		t.Errorf("UUIDv7 keyword should be case-insensitive, got %q", got3)
	}

	// A fresh value is generated per occurrence.
	first, _ := render("{uuidv7}", resolve)
	second, _ := render("{uuidv7}", resolve)
	if first == second {
		t.Errorf("expected distinct uuids per render, got %q twice", first)
	}
}

func TestFieldResolverNestedBodyAndFingerprint(t *testing.T) {
	body := map[string]any{
		"customer": map[string]any{"id": "c1"},
		"items":    []any{map[string]any{"sku": "a"}, map[string]any{"sku": "b", "qty": float64(2)}},
		"note":     nil,
	}
	req := httptest.NewRequest(http.MethodPost, "/orders/o1?q=x", nil)
	req.Header.Set("X-Tenant", "t1")
	resolve := sourceOf(req, map[string]string{"orderId": "o1"}, body).resolve

	for name, want := range map[string]string{
		"body.customer.id":  "c1",
		"body.items[1].sku": "b",
		"body.note":         "",
	} {
		if got, ok := resolve(name); !ok || got != want {
			t.Errorf("resolve(%q) = %q, %v; want %q", name, got, ok, want)
		}
	}

	for _, name := range []string{"body.items[5]", "body.customer[0]", "body.items.sku", "body.missing.x", "fingerprint(body.nope)", "not a placeholder"} {
		if got, ok := resolve(name); ok {
			t.Errorf("resolve(%q) = %q, true; want unresolved", name, got)
		}
	}

	whole, ok := resolve("fingerprint(body)")
	if !ok || len(whole) != 16 {
		t.Fatalf("fingerprint(body) = %q, %v; want 16 hex chars", whole, ok)
	}
	item, _ := resolve("fingerprint(body.items[1])")
	if item == whole {
		t.Error("fingerprint of a sub-field equals the whole body's")
	}
	if again, _ := resolve("fingerprint(body)"); again != whole {
		t.Errorf("fingerprint(body) not stable: %q then %q", whole, again)
	}
	for _, name := range []string{"fingerprint(path.orderId)", "fingerprint(query.q)", "fingerprint(header.X-Tenant)"} {
		if got, ok := resolve(name); !ok || len(got) != 16 {
			t.Errorf("resolve(%q) = %q, %v; want a fingerprint", name, got, ok)
		}
	}

	if _, ok := sourceOf(req, nil, nil).resolve("fingerprint(body)"); ok {
		t.Error("fingerprint(body) with no body: want unresolved")
	}
}

func TestFingerprintIgnoresKeyOrderAndNumberSpelling(t *testing.T) {
	a := fingerprint(map[string]any{"a": float64(1), "b": "x"})
	b := fingerprint(map[string]any{"b": "x", "a": 1.0})
	if a != b {
		t.Fatalf("fingerprints differ for equal documents: %q vs %q", a, b)
	}
	if a == fingerprint(map[string]any{"a": float64(2), "b": "x"}) {
		t.Fatal("different documents share a fingerprint")
	}
	// Known value, so the format can't drift silently: sha256(`"x"`)[:8].
	if got := fingerprint("x"); got != "ba2df4903a2c14e8" {
		t.Fatalf(`fingerprint("x") = %q, want "ba2df4903a2c14e8"`, got)
	}
}

// sourceOf builds the Source the gateway would for r.
func sourceOf(r *http.Request, pathParams map[string]string, body any) Source {
	return Source{PathParams: pathParams, Query: r.URL.Query(), Header: r.Header, Body: body}
}

func TestRenderResolvesFromSource(t *testing.T) {
	src := Source{PathParams: map[string]string{"orderId": "o1"}, Body: map[string]any{"items": []any{"a"}}}
	got, unresolved := Render("order-{path.orderId}-{body.items[0]}-{body.missing}", src)
	if got != "order-o1-a-{body.missing}" || len(unresolved) != 1 || unresolved[0] != "{body.missing}" {
		t.Fatalf("Render = %q, %v", got, unresolved)
	}
}

func TestResolveMissingValues(t *testing.T) {
	resolve := sourceOf(httptest.NewRequest(http.MethodGet, "/", nil), nil, map[string]any{"empty": nil}).resolve
	for _, name := range []string{"query.absent", "header.Absent"} {
		if _, ok := resolve(name); ok {
			t.Errorf("resolve(%q) ok = true, want false", name)
		}
	}
	if v, ok := resolve("body.empty"); !ok || v != "" {
		t.Errorf(`resolve("body.empty") = %q, %v; want "", true`, v, ok)
	}
}
