// Package temporal wires the gateway to one or more Temporal namespaces
// (see Connections): dialing a client per namespace and dispatching each
// x-temporal action (startWorkflow, signalWorkflow, queryWorkflow,
// cancelWorkflow, terminateWorkflow, getResult) against the connection its
// binding names. Catalog supplies a workflow type's default task queue for
// bindings that don't set their own.
package temporal

import (
	"crypto/tls"
	"fmt"

	"go.temporal.io/sdk/client"
	sdkotel "go.temporal.io/sdk/contrib/opentelemetry"

	"temporal-gateway/internal/config"
)

// NewClient dials the Temporal namespace described by cfg. Every call the
// returned client makes (ExecuteWorkflow, SignalWorkflow, ...) is wrapped
// with go.temporal.io/sdk/contrib/opentelemetry's tracing interceptor,
// which reads the current span from the call's context.Context and
// propagates its trace context into the Temporal request's Header - so a
// span internal/gateway starts for an incoming HTTP request carries through
// to the workflow it dispatches (see internal/telemetry for how that
// span's TracerProvider is configured). The interceptor uses the
// process-global TracerProvider, so this is a safe no-op when telemetry is
// disabled.
func NewClient(cfg config.TemporalConnectionConfig) (client.Client, error) {
	tracingInterceptor, err := sdkotel.NewTracingInterceptor(sdkotel.TracerOptions{})
	if err != nil {
		return nil, fmt.Errorf("temporal: build tracing interceptor: %w", err)
	}

	options := client.Options{
		HostPort:  cfg.Host,
		Namespace: cfg.Namespace,
	}
	options.Interceptors = append(options.Interceptors, tracingInterceptor)

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
		return nil, fmt.Errorf("temporal: dial %q: %w", cfg.Host, err)
	}
	return c, nil
}
