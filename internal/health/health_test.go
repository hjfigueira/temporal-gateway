package health

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func probe(t *testing.T, p *Probes, path string) (int, probeResponse) {
	t.Helper()
	rec := httptest.NewRecorder()
	p.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	var body probeResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %s body %q: %v", path, rec.Body.String(), err)
	}
	return rec.Code, body
}

func checks(results map[string]error) CheckFunc {
	return func(context.Context) map[string]error { return results }
}

func TestLivenessAlwaysPasses(t *testing.T) {
	p := NewProbes("waiting for temporal")
	if code, _ := probe(t, p, "/livez"); code != http.StatusOK {
		t.Fatalf("/livez while not ready = %d, want 200", code)
	}
}

func TestReadiness(t *testing.T) {
	p := NewProbes("waiting for temporal")
	if code, body := probe(t, p, "/readyz"); code != http.StatusServiceUnavailable || body.Reason != "waiting for temporal" {
		t.Fatalf("/readyz before Ready = %d %+v, want 503 with reason", code, body)
	}

	p.Ready(checks(map[string]error{"default": nil, "notifications": nil}))
	if code, body := probe(t, p, "/readyz"); code != http.StatusOK || body.Checks["default"] != "ok" {
		t.Fatalf("/readyz all healthy = %d %+v, want 200", code, body)
	}

	p.Ready(checks(map[string]error{"default": nil, "notifications": errors.New("unavailable")}))
	if code, body := probe(t, p, "/readyz"); code != http.StatusServiceUnavailable || body.Checks["notifications"] != "unavailable" {
		t.Fatalf("/readyz one unhealthy = %d %+v, want 503 naming the backend", code, body)
	}

	p.NotReady("shutting down")
	if code, body := probe(t, p, "/readyz"); code != http.StatusServiceUnavailable || body.Reason != "shutting down" {
		t.Fatalf("/readyz after NotReady = %d %+v, want 503", code, body)
	}
}
