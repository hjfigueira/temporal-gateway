// Package health serves the gateway's Kubernetes-style probes on their own
// port, separate from the API spec's routes:
//
//   - GET /livez passes whenever the process can answer HTTP at all, so an
//     orchestrator restarts the gateway only when it's actually wedged - not
//     while it's waiting for Temporal (see ADR-018) or while Temporal is down.
//   - GET /readyz passes only once every Temporal namespace is connected and
//     currently answers a health check, so traffic isn't routed to a gateway
//     that can only respond with errors. It fails again during shutdown.
package health

import (
	"context"
	"encoding/json"
	"net/http"
	"sync/atomic"
	"time"
)

// checkTimeout bounds one /readyz evaluation, so a hung backend shows up as
// not-ready rather than as a probe timeout.
const checkTimeout = 2 * time.Second

// CheckFunc reports the health of each required backend, keyed by name
// (e.g. Temporal namespace); a nil error means that backend is healthy.
type CheckFunc func(ctx context.Context) map[string]error

// readiness is either "not ready, because reason" (check == nil) or
// "ready as long as check passes".
type readiness struct {
	reason string
	check  CheckFunc
}

// Probes holds the gateway's readiness state and serves both probes. It
// starts not ready; safe for concurrent use.
type Probes struct {
	state atomic.Pointer[readiness]
}

// NewProbes returns Probes that report not ready with reason until Ready is
// called.
func NewProbes(reason string) *Probes {
	p := &Probes{}
	p.NotReady(reason)
	return p
}

// Ready makes /readyz pass whenever check reports every backend healthy.
func (p *Probes) Ready(check CheckFunc) {
	p.state.Store(&readiness{check: check})
}

// NotReady makes /readyz fail with reason, regardless of backend health.
func (p *Probes) NotReady(reason string) {
	p.state.Store(&readiness{reason: reason})
}

// Handler serves GET /livez and GET /readyz.
func (p *Probes) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /livez", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, probeResponse{Status: "ok"})
	})
	mux.HandleFunc("GET /readyz", p.serveReady)
	return mux
}

type probeResponse struct {
	Status string            `json:"status"`
	Reason string            `json:"reason,omitempty"`
	Checks map[string]string `json:"checks,omitempty"`
}

func (p *Probes) serveReady(w http.ResponseWriter, r *http.Request) {
	state := p.state.Load()
	if state.check == nil {
		writeJSON(w, http.StatusServiceUnavailable, probeResponse{Status: "not_ready", Reason: state.reason})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), checkTimeout)
	defer cancel()

	resp := probeResponse{Status: "ready", Checks: map[string]string{}}
	code := http.StatusOK
	for name, err := range state.check(ctx) {
		if err != nil {
			resp.Checks[name] = err.Error()
			resp.Status = "not_ready"
			code = http.StatusServiceUnavailable
			continue
		}
		resp.Checks[name] = "ok"
	}
	writeJSON(w, code, resp)
}

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}
