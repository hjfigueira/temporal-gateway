// Package temporal wires the gateway to a real Temporal cluster: dialing a
// client and dispatching each x-temporal action (startWorkflow,
// signalWorkflow, queryWorkflow, cancelWorkflow, terminateWorkflow,
// getResult) against it. Catalog supplies a workflow type's default task
// queue for bindings that don't set their own.
package temporal

import (
	"crypto/tls"
	"fmt"

	"go.temporal.io/sdk/client"

	"temporal-gateway/internal/config"
)

// NewClient dials the Temporal cluster described by cfg.
func NewClient(cfg config.TemporalConfig) (client.Client, error) {
	options := client.Options{
		HostPort:  cfg.HostPort,
		Namespace: cfg.Namespace,
	}

	if cfg.TLS.Enabled {
		tlsConfig := &tls.Config{}
		if cfg.TLS.CertPath != "" && cfg.TLS.KeyPath != "" {
			cert, err := tls.LoadX509KeyPair(cfg.TLS.CertPath, cfg.TLS.KeyPath)
			if err != nil {
				return nil, fmt.Errorf("temporal: load TLS keypair: %w", err)
			}
			tlsConfig.Certificates = []tls.Certificate{cert}
		}
		options.ConnectionOptions = client.ConnectionOptions{TLS: tlsConfig}
	}

	c, err := client.Dial(options)
	if err != nil {
		return nil, fmt.Errorf("temporal: dial %q: %w", cfg.HostPort, err)
	}
	return c, nil
}
