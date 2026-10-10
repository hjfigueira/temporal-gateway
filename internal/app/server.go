package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"temporal-gateway/internal/gateway"
	"temporal-gateway/internal/gateway/health"
	"temporal-gateway/internal/temporal"
)

// Health builds State.Probes - readiness reports each namespace as "not
// connected yet" until it's dialed, then its live health (ADR-019, ADR-026)
// - and, when enabled, serves them on their own port.
func Health() Module {
	var server *http.Server
	return Module{
		Init: func(_ context.Context, s *State) error {
			s.Probes = health.NewProbes("starting")
			s.Probes.Ready(s.Connections.CheckHealth)
			if !s.Config.Health.Enabled {
				return nil
			}
			var err error
			server, err = startHealthServer(fmt.Sprintf("%s:%d", s.Config.Health.Host, s.Config.Health.Port), s.Probes.Handler(), s.Logger)
			if err != nil {
				return fmt.Errorf("start health server: %w", err)
			}
			return nil
		},
		Stop: func(context.Context, *State) {
			// Close only fails if closing the listener does; nothing to do then.
			if server != nil {
				_ = server.Close()
			}
		},
	}
}

// API serves the spec's routes until shutdown, then marks the gateway not
// ready and drains in-flight requests within State.ShutdownTimeout.
func API() Module {
	var server *http.Server
	return Module{
		Init: func(_ context.Context, s *State) error {
			requestTimeout := s.Config.Server.RequestTimeoutDuration()
			handler := gateway.NewHandler(s.Spec, temporal.NewDispatcher(s.Connections), gateway.Options{
				MaxBodyBytes:   s.Config.Server.MaxBodyBytesOrDefault(),
				RequestTimeout: requestTimeout,
			}, s.Logger)
			server = newServer(fmt.Sprintf("%s:%d", s.Config.Server.Host, s.Config.Server.Port), handler, requestTimeout)
			return nil
		},
		Run: func(ctx context.Context, s *State) error {
			return serve(ctx, server, s.Probes, s.ShutdownTimeout, s.Logger)
		},
		// serve has already shut server down gracefully; this only releases
		// what's left, and has nothing actionable to report.
		Stop: func(context.Context, *State) { _ = server.Close() },
	}
}

// newServer builds the HTTP server fronting handler, with timeouts bounding
// how long a client may hold a connection open at each stage. The write
// timeout trails requestTimeout, so a slow dispatch is cut off by the
// handler's own deadline (and answered with 504) before the server drops
// the response.
func newServer(addr string, handler http.Handler, requestTimeout time.Duration) *http.Server {
	return &http.Server{
		Addr:    addr,
		Handler: handler,
		// ReadHeaderTimeout bounds how long a client may take sending
		// request headers, so a slow/stalled client can't tie up a
		// connection indefinitely (a "slowloris" resource-exhaustion risk
		// with the net/http default of no timeout).
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      requestTimeout + 5*time.Second,
		// IdleTimeout closes keep-alive connections that go quiet instead
		// of holding their read/write buffers open indefinitely.
		IdleTimeout: 60 * time.Second,
	}
}

// startHealthServer binds addr synchronously - so a port conflict fails
// startup instead of being logged from a goroutine - then serves handler on
// it in the background until the returned server is closed.
func startHealthServer(addr string, handler http.Handler, logger *slog.Logger) (*http.Server, error) {
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	server := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      10 * time.Second,
	}
	// Serve on an open listener only returns once the server is closed.
	go func() { _ = server.Serve(listener) }()
	logger.Info("starting health server", "addr", listener.Addr().String())
	return server, nil
}

// serve runs server until ctx is cancelled (SIGINT/SIGTERM, or another
// module's Run failing), then marks the gateway not ready and shuts server
// down gracefully, so in-flight requests are drained instead of dropped,
// within shutdownTimeout.
func serve(ctx context.Context, server *http.Server, probes *health.Probes, shutdownTimeout time.Duration, logger *slog.Logger) error {
	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		<-ctx.Done()
		probes.NotReady("shutting down")
		logger.Info("shutting down", "timeout", shutdownTimeout.String())
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			logger.Error("graceful shutdown failed", "error", err)
		}
	}()

	logger.Info("starting http server", "addr", server.Addr)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("http server stopped: %w", err)
	}
	// ListenAndServe returns as soon as Shutdown starts; wait for the drain
	// to finish, or the Stop hooks would cut in-flight requests off.
	<-shutdownDone
	return nil
}
