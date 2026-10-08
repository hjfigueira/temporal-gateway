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
	// NexusEndpoint mirrors config.TemporalConnectionConfig.NexusEndpoint -
	// empty when this namespace isn't reachable via Nexus.
	NexusEndpoint string
	// DispatchTaskQueue mirrors config.TemporalConnectionConfig.
	// DispatchTaskQueue(): the task queue this namespace's Dispatch Nexus
	// service worker polls (see BuildNexusWorkers), already defaulted.
	// Meaningless when DispatchExternal is set.
	DispatchTaskQueue string
	// DispatchExternal mirrors config.TemporalConnectionConfig.
	// NexusDispatchExternal: when true, BuildNexusWorkers does not host a
	// Dispatch worker for this namespace - some other service does.
	DispatchExternal bool
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
		conns[c.Namespace] = &Connection{
			Client:            cl,
			Catalog:           NewCatalog(c.Workflows),
			NexusEndpoint:     c.NexusEndpoint,
			DispatchTaskQueue: c.DispatchTaskQueue(),
			DispatchExternal:  c.NexusDispatchExternal,
		}
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

// NexusEndpoints returns the configured Nexus endpoint name for every
// namespace that has one (config.TemporalConnectionConfig.NexusEndpoint),
// keyed by namespace - used by the "nexus" driver (see
// internal/gateway.NexusDriver) to resolve which endpoint reaches a given
// trigger's target namespace.
func (c Connections) NexusEndpoints() map[string]string {
	endpoints := make(map[string]string, len(c))
	for namespace, conn := range c {
		if conn.NexusEndpoint != "" {
			endpoints[namespace] = conn.NexusEndpoint
		}
	}
	return endpoints
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

// ValidateNexusConfig checks every operation using x-temporal.driver: nexus
// against conns: its x-temporal.config.namespace must resolve to a
// configured connection, and every one of its triggers' own namespace must
// have a config.TemporalConnectionConfig.NexusEndpoint configured, since
// that's how the CascadeEvent workflow this driver starts (see
// CascadeEvent) reaches it. Called once at startup alongside
// ValidateNamespaces, for the same reason: fail before serving traffic
// rather than on the first request that hits the route.
func ValidateNexusConfig(apiSpec *spec.Spec, conns Connections) error {
	var errs []error
	for _, route := range apiSpec.Routes() {
		t := route.Operation.Temporal
		if t.DriverOrDefault() != spec.DriverNexus {
			continue
		}

		if t.Config == nil {
			// Already reported by spec.Spec.validate (driver nexus
			// requires config) - nothing more to check here.
			continue
		}

		if _, ok := conns[t.Config.Namespace]; !ok {
			errs = append(errs, fmt.Errorf("%s %s: x-temporal.config: namespace %q has no temporal.connections entry in config", route.Method, route.Path, t.Config.Namespace))
		}

		for i, trigger := range t.Triggers {
			conn, ok := conns[trigger.Namespace]
			if !ok {
				continue // already reported by ValidateNamespaces
			}
			if conn.NexusEndpoint == "" {
				errs = append(errs, fmt.Errorf("%s %s: x-temporal.triggers[%d]: namespace %q has no nexusEndpoint configured in temporal.connections, required for the nexus driver", route.Method, route.Path, i, trigger.Namespace))
			}
		}
	}
	return errors.Join(errs...)
}
