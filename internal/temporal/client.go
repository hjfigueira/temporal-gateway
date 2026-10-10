// Package temporal wires the gateway to one or more Temporal namespaces
// (see Connections): dialing a client per namespace and dispatching each
// x-temporal action (startWorkflow, signalWorkflow, queryWorkflow,
// cancelWorkflow, terminateWorkflow, getResult) against the connection its
// binding names. Catalog supplies a workflow type's default task queue for
// bindings that don't set their own.
package temporal

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log/slog"
	"os"
	"time"

	"go.temporal.io/sdk/client"
	sdkotel "go.temporal.io/sdk/contrib/opentelemetry"
	"google.golang.org/grpc"

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
//
// If the dial itself fails (Temporal unreachable), it is retried every
// reconnect.IntervalDuration() until it succeeds, reconnect.MaxAttempts is
// reached (0 = unlimited), or ctx is cancelled - so a gateway started
// before Temporal is up waits for it instead of exiting. Errors building
// the client options (e.g. an unreadable TLS keypair) are configuration
// mistakes no retry can fix, so they're returned immediately.
func NewClient(ctx context.Context, cfg config.TemporalConnectionConfig, reconnect config.TemporalReconnectConfig, logger *slog.Logger) (client.Client, error) {
	options, err := clientOptions(cfg)
	if err != nil {
		return nil, err
	}
	dial := func(ctx context.Context) (client.Client, error) {
		return client.DialContext(ctx, options)
	}
	c, err := dialWithRetry(ctx, dial, reconnect.IntervalDuration(), reconnect.MaxAttempts, logger.With("namespace", cfg.Namespace, "host", cfg.Host))
	if err != nil {
		return nil, fmt.Errorf("temporal: dial %q: %w", cfg.Host, err)
	}
	return c, nil
}

// clientOptions builds the SDK client.Options for cfg: host, namespace,
// tracing interceptor, the gRPC interceptor reporting whether a start
// created a new run (see started.go), TLS, and API-key credentials.
func clientOptions(cfg config.TemporalConnectionConfig) (client.Options, error) {
	// The error is always nil: NewTracer only fills in defaults
	// (contrib/opentelemetry v0.8.1).
	tracingInterceptor, _ := sdkotel.NewTracingInterceptor(sdkotel.TracerOptions{})

	options := client.Options{
		HostPort:  cfg.Host,
		Namespace: cfg.Namespace,
		ConnectionOptions: client.ConnectionOptions{
			DialOptions: []grpc.DialOption{grpc.WithChainUnaryInterceptor(startedInterceptor)},
		},
	}
	options.Interceptors = append(options.Interceptors, tracingInterceptor)

	if cfg.TLS.Enabled {
		tlsConfig, err := tlsConfig(cfg.TLS)
		if err != nil {
			return client.Options{}, err
		}
		options.ConnectionOptions.TLS = tlsConfig
	}

	// API-key credentials make the SDK enable TLS on its own when TLS is
	// not configured explicitly.
	if cfg.APIKey != "" {
		options.Credentials = client.NewAPIKeyStaticCredentials(cfg.APIKey)
	}

	return options, nil
}

// tlsConfig builds the tls.Config for cfg: an optional mTLS client keypair,
// an optional CA bundle replacing the system roots, and an optional
// server-name override.
func tlsConfig(cfg config.TemporalTLSConfig) (*tls.Config, error) {
	tlsConfig := &tls.Config{ServerName: cfg.ServerName}
	if cfg.CertPath != "" && cfg.KeyPath != "" {
		cert, err := tls.LoadX509KeyPair(cfg.CertPath, cfg.KeyPath)
		if err != nil {
			return nil, fmt.Errorf("temporal: load TLS keypair: %w", err)
		}
		tlsConfig.Certificates = []tls.Certificate{cert}
	}
	if cfg.CAPath != "" {
		pem, err := os.ReadFile(cfg.CAPath)
		if err != nil {
			return nil, fmt.Errorf("temporal: read TLS CA bundle: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("temporal: TLS CA bundle %q contains no PEM certificates", cfg.CAPath)
		}
		tlsConfig.RootCAs = pool
	}
	return tlsConfig, nil
}

// dialWithRetry calls dial until it succeeds, waiting interval between
// attempts and logging each failure. It gives up after maxAttempts attempts
// when maxAttempts > 0, or as soon as ctx is cancelled (e.g. on SIGINT/
// SIGTERM while waiting for Temporal to come up).
func dialWithRetry(ctx context.Context, dial func(context.Context) (client.Client, error), interval time.Duration, maxAttempts int, logger *slog.Logger) (client.Client, error) {
	for attempt := 1; ; attempt++ {
		c, err := dial(ctx)
		if err == nil {
			if attempt > 1 {
				logger.Info("connected to temporal", "attempt", attempt)
			}
			return c, nil
		}
		if ctx.Err() != nil {
			return nil, fmt.Errorf("gave up after %d attempt(s): %w", attempt, ctx.Err())
		}
		if maxAttempts > 0 && attempt >= maxAttempts {
			return nil, fmt.Errorf("gave up after %d attempt(s): %w", attempt, err)
		}

		logger.Warn("temporal unavailable, retrying",
			"attempt", attempt,
			"max_attempts", maxAttempts,
			"retry_in", interval.String(),
			"error", err,
		)

		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, fmt.Errorf("gave up after %d attempt(s): %w", attempt, ctx.Err())
		case <-timer.C:
		}
	}
}
