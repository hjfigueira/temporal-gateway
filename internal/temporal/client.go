// Package temporal wires the gateway to a real Temporal cluster: dialing a
// client, validating the API spec against the configured workflow catalog,
// and dispatching each x-temporal action (start/signal/query/etc a
// workflow) against it.
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
