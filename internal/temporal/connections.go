package temporal

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"

	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"

	"temporal-gateway/internal/config"
	"temporal-gateway/internal/spec"
)

// Connection is one configured Temporal namespace: its workflow catalog
// and, once Connect has dialed it, its client. Requests can arrive before
// that (the HTTP server starts first - ADR-026), so the client is read
// through Client and swapped in atomically.
type Connection struct {
	Catalog *Catalog

	cfg    config.TemporalConnectionConfig
	client atomic.Pointer[client.Client]
}

// Client returns the namespace's dialed client, or nil while it is still
// connecting.
func (c *Connection) Client() client.Client {
	if p := c.client.Load(); p != nil {
		return *p
	}
	return nil
}

func (c *Connection) setClient(cl client.Client) { c.client.Store(&cl) }

// Connections is the temporalConnection dictionary: every Temporal
// namespace the gateway is configured to talk to, keyed by namespace name.
// Each x-temporal binding names which entry it targets via its own
// Namespace field (see spec.TemporalBinding), so a single gateway process
// can dispatch actions against several namespaces - even several different
// clusters - at once. The map itself is built once and never changes after
// that (only each entry's client is filled in), so it's safe to share
// across the concurrent dispatches in internal/gateway.
type Connections map[string]*Connection

// errNotConnected is a namespace's readiness result while Connect is still
// dialing it.
var errNotConnected = errors.New("not connected yet")

// newClient is NewClient, swappable in tests.
var newClient = NewClient

// NewConnections builds one not-yet-connected entry per cfg.Connections;
// Connect dials them.
func NewConnections(cfg config.TemporalConfig) Connections {
	conns := make(Connections, len(cfg.Connections))
	for _, c := range cfg.Connections {
		conns[c.Namespace] = &Connection{Catalog: NewCatalog(c.Workflows), cfg: c}
	}
	return conns
}

// Connect dials every namespace concurrently, retrying each unreachable one
// per reconnect (see NewClient). Each namespace becomes usable the moment
// its own dial succeeds, so one slow namespace doesn't hold up the others.
// It returns once every dial has finished, joining every failure.
func (c Connections) Connect(ctx context.Context, reconnect config.TemporalReconnectConfig, logger *slog.Logger) error {
	var wg sync.WaitGroup
	errs := make(chan error, len(c))
	for namespace, conn := range c {
		wg.Go(func() {
			cl, err := newClient(ctx, conn.cfg, reconnect, logger)
			if err != nil {
				errs <- fmt.Errorf("temporal: namespace %q: %w", namespace, err)
				return
			}
			conn.setClient(cl)
			logger.Info("connected to temporal", "namespace", namespace, "host", conn.cfg.Host)
		})
	}
	wg.Wait()
	close(errs)
	var all []error
	for err := range errs {
		all = append(all, err)
	}
	return errors.Join(all...)
}

// resolve looks up the connection for namespace, once it's connected (a
// client only ever goes from nil to set, so callers may then use
// conn.Client() freely). A namespace that's configured but still connecting
// reports Temporal's own Unavailable error, which the gateway answers with
// 503, the same as Temporal being down. An unconfigured namespace can't
// happen for a spec ValidateNamespaces accepted, but is reported rather
// than assumed.
func (c Connections) resolve(namespace string) (*Connection, error) {
	conn, ok := c[namespace]
	if !ok {
		return nil, fmt.Errorf("temporal: no connection configured for namespace %q", namespace)
	}
	if conn.Client() == nil {
		return nil, serviceerror.NewUnavailable(fmt.Sprintf("temporal namespace %q is not connected yet", namespace))
	}
	return conn, nil
}

// Close closes every client dialed so far.
func (c Connections) Close() {
	for _, conn := range c {
		if cl := conn.Client(); cl != nil {
			cl.Close()
		}
	}
}

// CheckHealth asks every namespace's Temporal frontend whether it's serving,
// concurrently, returning each namespace's result (nil = healthy). Used by
// the readiness probe (see internal/gateway/health).
func (c Connections) CheckHealth(ctx context.Context) map[string]error {
	var (
		mu      sync.Mutex
		wg      sync.WaitGroup
		results = make(map[string]error, len(c))
	)
	for namespace, conn := range c {
		wg.Go(func() {
			err := errNotConnected
			if cl := conn.Client(); cl != nil {
				_, err = cl.CheckHealth(ctx, &client.CheckHealthRequest{})
			}
			mu.Lock()
			results[namespace] = err
			mu.Unlock()
		})
	}
	wg.Wait()
	return results
}

// ValidateNamespaces checks that every x-temporal binding in apiSpec names
// a namespace present in conns, joining every problem found (rather than
// stopping at the first) so every bad reference is reported in one pass.
// Called once at startup, so a spec referencing an unconfigured namespace
// fails before the server starts serving requests rather than as a
// per-request dispatch error the first time that route is hit.
func ValidateNamespaces(apiSpec *spec.Spec, conns Connections) error {
	var errs []error
	for _, route := range apiSpec.Routes() {
		for i, binding := range route.Operation.Temporal.Triggers {
			if _, ok := conns[binding.Namespace]; !ok {
				errs = append(errs, fmt.Errorf("%s %s: x-temporal.triggers[%d]: namespace %q has no temporal.connections entry in config", route.Method, route.Path, i, binding.Namespace))
			}
		}
	}
	return errors.Join(errs...)
}
