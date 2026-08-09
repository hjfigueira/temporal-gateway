package config

import "fmt"

type TemporalTLSConfig struct {
	Enabled  bool   `yaml:"enabled"`
	CertPath string `yaml:"certPath,omitempty"`
	KeyPath  string `yaml:"keyPath,omitempty"`
}

// WorkflowDefinition documents a workflow type the gateway addresses: its
// default task queue (used when an x-temporal binding doesn't set its own
// taskQueue) and, informationally, the signals/queries it exposes. It is not
// cross-checked against the API spec's x-temporal bindings; an unknown
// workflow, signal, or query surfaces as a Temporal error at request time.
type WorkflowDefinition struct {
	Name      string   `yaml:"name"`
	TaskQueue string   `yaml:"taskQueue"`
	Signals   []string `yaml:"signals,omitempty"`
	Queries   []string `yaml:"queries,omitempty"`
}

// DefaultNexusDispatchTaskQueue is the task queue a namespace's Dispatch
// Nexus service worker polls (see internal/temporal.BuildNexusWorkers) when
// TemporalConnectionConfig.NexusDispatchTaskQueue isn't set. Whatever value
// is actually in effect must match the target task queue a Nexus endpoint
// reaching this namespace was provisioned with (endpoints are a
// server-side resource - see the "nexus" driver docs in README.md).
const DefaultNexusDispatchTaskQueue = "temporal-gateway-nexus-dispatch"

// TemporalConnectionConfig is one Temporal namespace the gateway dials a
// client for. The gateway can serve routes against several namespaces (even
// on different clusters) at once - see internal/temporal.Connections; each
// x-temporal binding in the API spec names which one it targets via its own
// namespace field, which must match this Namespace.
type TemporalConnectionConfig struct {
	Namespace string               `yaml:"namespace"`
	Host      string               `yaml:"host"`
	TLS       TemporalTLSConfig    `yaml:"tls"`
	Workflows []WorkflowDefinition `yaml:"workflows"`
	// NexusEndpoint names the Temporal Nexus endpoint (a server-side
	// resource, provisioned separately - e.g. via `temporal operator nexus
	// endpoint create`) that reaches this namespace. Required on every
	// namespace an x-temporal.triggers entry targets when its operation
	// uses the "nexus" driver (see internal/temporal.ValidateNexusConfig);
	// unused otherwise.
	NexusEndpoint string `yaml:"nexusEndpoint,omitempty"`
	// NexusDispatchTaskQueue is the task queue this namespace's Dispatch
	// Nexus service worker polls - see DispatchTaskQueue.
	NexusDispatchTaskQueue string `yaml:"nexusDispatchTaskQueue,omitempty"`
}

// DispatchTaskQueue returns c.NexusDispatchTaskQueue, defaulting to
// DefaultNexusDispatchTaskQueue when it wasn't set.
func (c TemporalConnectionConfig) DispatchTaskQueue() string {
	if c.NexusDispatchTaskQueue == "" {
		return DefaultNexusDispatchTaskQueue
	}
	return c.NexusDispatchTaskQueue
}

// TemporalConfig lists every Temporal namespace connection the gateway
// dials at startup.
type TemporalConfig struct {
	Connections []TemporalConnectionConfig `yaml:"connections"`
}

// validate checks that temporal.connections declares at least one entry and
// that every entry has a non-empty, unique namespace - the key
// internal/temporal.Connections and each x-temporal binding's namespace
// field resolve a connection by.
func (c TemporalConfig) validate() error {
	if len(c.Connections) == 0 {
		return fmt.Errorf("temporal.connections requires at least one entry")
	}

	seen := make(map[string]bool, len(c.Connections))
	for i, conn := range c.Connections {
		if conn.Namespace == "" {
			return fmt.Errorf("temporal.connections[%d]: missing namespace", i)
		}
		if seen[conn.Namespace] {
			return fmt.Errorf("temporal.connections[%d]: duplicate namespace %q", i, conn.Namespace)
		}
		seen[conn.Namespace] = true
	}
	return nil
}
