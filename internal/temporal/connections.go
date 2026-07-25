package temporal

import (
	"errors"
	"fmt"

	"go.temporal.io/sdk/client"

	"temporal-gateway/internal/config"
	"temporal-gateway/internal/spec"
)

// Connection is one namespace's dialed Temporal client, paired with the
// workflow catalog declared alongside it in config.yml.
type Connection struct {
	Client  client.Client
	Catalog *Catalog
}

// Connections is the temporalConnection dictionary: every Temporal
// namespace the gateway is configured to talk to, keyed by namespace name.
// Each x-temporal binding names which entry it targets via its own
// Namespace field (see spec.TemporalBinding), so a single gateway process
// can dispatch actions against several namespaces - even several different
// clusters - at once. Built once at startup from config.yml's
// temporal.connections and read-only from then on, so it's safe to share
// across the concurrent dispatches in internal/gateway.
type Connections map[string]*Connection

// NewConnections dials one client per entry in cfg.Connections. On error,
// it still returns whatever connections it managed to dial before the
// failure so the caller can Close them.
func NewConnections(cfg config.TemporalConfig) (Connections, error) {
	conns := make(Connections, len(cfg.Connections))
	for _, c := range cfg.Connections {
		cl, err := NewClient(c)
		if err != nil {
			return conns, fmt.Errorf("temporal: namespace %q: %w", c.Namespace, err)
		}
		conns[c.Namespace] = &Connection{Client: cl, Catalog: NewCatalog(c.Workflows)}
	}
	return conns, nil
}

// resolve looks up the connection for namespace, returning an error naming
// the unresolved namespace if none was configured for it. In normal
// operation this never fails for a namespace ValidateNamespaces has already
// checked against the loaded API spec.
func (c Connections) resolve(namespace string) (*Connection, error) {
	conn, ok := c[namespace]
	if !ok {
		return nil, fmt.Errorf("temporal: no connection configured for namespace %q", namespace)
	}
	return conn, nil
}

// Close closes every dialed client.
func (c Connections) Close() {
	for _, conn := range c {
		conn.Client.Close()
	}
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
