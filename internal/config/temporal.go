package config

import (
	"fmt"
	"time"
)

// defaultReconnectInterval is how long the gateway waits between attempts to
// dial an unreachable Temporal namespace when temporal.reconnect.interval
// isn't set.
const defaultReconnectInterval = 5 * time.Second

// TemporalTLSConfig configures TLS to a Temporal frontend. CertPath/KeyPath
// are an optional mTLS client keypair (both or neither); CAPath is a PEM
// bundle trusted instead of the system roots (private-CA clusters);
// ServerName overrides the name verified against the server certificate.
type TemporalTLSConfig struct {
	Enabled    bool   `yaml:"enabled"`
	CertPath   string `yaml:"certPath,omitempty"`
	KeyPath    string `yaml:"keyPath,omitempty"`
	CAPath     string `yaml:"caPath,omitempty"`
	ServerName string `yaml:"serverName,omitempty"`
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

// TemporalConnectionConfig is one Temporal namespace the gateway dials a
// client for. The gateway can serve routes against several namespaces (even
// on different clusters) at once - see internal/temporal.Connections; each
// x-temporal binding in the API spec names which one it targets via its own
// namespace field, which must match this Namespace.
type TemporalConnectionConfig struct {
	Namespace string            `yaml:"namespace"`
	Host      string            `yaml:"host"`
	TLS       TemporalTLSConfig `yaml:"tls"`
	// APIKey authenticates to Temporal (e.g. Temporal Cloud) with an API
	// key; it implies TLS. Set it via "${VAR}" rather than in plain text,
	// and never log it.
	APIKey    string               `yaml:"apiKey,omitempty"`
	Workflows []WorkflowDefinition `yaml:"workflows"`
}

// TemporalReconnectConfig controls how the gateway retries dialing a
// Temporal namespace that isn't reachable at startup, instead of exiting on
// the first failed dial. Interval is a time.ParseDuration string (e.g. "5s")
// waited between attempts; MaxAttempts caps the total number of dial
// attempts per namespace, with 0 (the default) meaning retry until the dial
// succeeds or the process receives a shutdown signal.
type TemporalReconnectConfig struct {
	Interval    string `yaml:"interval,omitempty"`
	MaxAttempts int    `yaml:"maxAttempts,omitempty"`
}

// IntervalDuration returns Interval parsed as a duration, or
// defaultReconnectInterval when Interval is unset. Only meaningful after
// validate has accepted Interval.
func (r TemporalReconnectConfig) IntervalDuration() time.Duration {
	if r.Interval == "" {
		return defaultReconnectInterval
	}
	d, _ := time.ParseDuration(r.Interval)
	return d
}

func (r TemporalReconnectConfig) validate() error {
	if r.Interval != "" {
		d, err := time.ParseDuration(r.Interval)
		if err != nil {
			return fmt.Errorf("temporal.reconnect.interval: %w", err)
		}
		if d <= 0 {
			return fmt.Errorf("temporal.reconnect.interval must be positive, got %q", r.Interval)
		}
	}
	if r.MaxAttempts < 0 {
		return fmt.Errorf("temporal.reconnect.maxAttempts must be >= 0 (0 means unlimited), got %d", r.MaxAttempts)
	}
	return nil
}

// TemporalConfig lists every Temporal namespace connection the gateway
// dials at startup, and how to retry those dials while Temporal is
// unreachable.
type TemporalConfig struct {
	Connections []TemporalConnectionConfig `yaml:"connections"`
	Reconnect   TemporalReconnectConfig    `yaml:"reconnect"`
}

// validate checks that temporal.connections declares at least one entry and
// that every entry has a non-empty, unique namespace - the key
// internal/temporal.Connections and each x-temporal binding's namespace
// field resolve a connection by - and that temporal.reconnect is well-formed.
func (c TemporalConfig) validate() error {
	if err := c.Reconnect.validate(); err != nil {
		return err
	}

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
		if (conn.TLS.CertPath == "") != (conn.TLS.KeyPath == "") {
			return fmt.Errorf("temporal.connections[%d]: tls.certPath and tls.keyPath must be set together", i)
		}
	}
	return nil
}
