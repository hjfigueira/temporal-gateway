// Command temporal-gateway reads a gateway config and its linked API
// specification, then generates HTTP endpoints in front of Temporal.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/joho/godotenv"

	"temporal-gateway/internal/config"
	"temporal-gateway/internal/gateway"
	"temporal-gateway/internal/gateway/health"
	"temporal-gateway/internal/spec"
	"temporal-gateway/internal/telemetry"
	"temporal-gateway/internal/temporal"
)

// shutdownTimeout bounds how long the server waits for in-flight requests to
// finish once a shutdown signal is received, and how long telemetry export
// gets to flush on the same shutdown. A var so tests can shorten it.
var shutdownTimeout = 10 * time.Second

// exit and setupTelemetry are swapped out by tests.
var (
	exit           = os.Exit
	setupTelemetry = telemetry.Setup
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	// Cancelled on SIGINT/SIGTERM: interrupts a startup that's still
	// waiting for Temporal to come up, and triggers the HTTP server's
	// graceful shutdown once it's serving.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, os.Args[1:], logger); err != nil {
		logger.Error("fatal", "error", err)
		exit(1)
	}
}

// run wires the gateway's dependencies and serves until a shutdown signal
// arrives. Every stage returns an error instead of exiting directly, so
// deferred cleanup (closing Temporal connections, flushing telemetry) always
// runs - os.Exit skips defers, which is why it's called exactly once, in
// main, after run has already returned and unwound.
func run(ctx context.Context, args []string, logger *slog.Logger) error {
	flags, err := parseFlags(args)
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	if err != nil {
		return err
	}

	// A missing .env is normal: most deployments set real environment
	// variables instead. Variables already set always win over the file.
	if err := godotenv.Load(flags.envPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("load env file %q: %w", flags.envPath, err)
	}

	cfg, err := config.Load(flags.configPath)
	if err != nil {
		return fmt.Errorf("load gateway config %q: %w", flags.configPath, err)
	}

	// Configure tracing before dialing Temporal or building the HTTP
	// handler, so both pick up the real TracerProvider from the start
	// rather than the no-op one otel installs by default.
	shutdownTelemetry, err := setupTelemetry(context.Background(), cfg.OTel)
	if err != nil {
		return fmt.Errorf("set up telemetry: %w", err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := shutdownTelemetry(shutdownCtx); err != nil {
			logger.Error("failed to shut down telemetry", "error", err)
		}
	}()
	logger.Info("configured telemetry", "otel_enabled", cfg.OTel.Enabled, "otel_endpoint", cfg.OTel.Endpoint)

	specPaths := cfg.ResolveSpecPaths()
	apiSpec, err := spec.Load(specPaths...)
	if err != nil {
		return fmt.Errorf("load api spec %v: %w", specPaths, err)
	}

	logStartup(logger, flags.configPath, specPaths, cfg, apiSpec)

	connections := temporal.NewConnections(cfg.Temporal)
	// Closes every client dialed by the time run returns.
	defer connections.Close()

	// Needs only config.yml's namespace names, so a bad reference fails
	// startup even while Temporal is unreachable.
	if err := temporal.ValidateNamespaces(apiSpec, connections); err != nil {
		return fmt.Errorf("api spec references unknown temporal namespace: %w", err)
	}

	if flags.dryRun {
		// A dry run is a pass/fail check (CI, pre-deploy): dial now, once,
		// instead of serving while retrying.
		reconnect := cfg.Temporal.Reconnect
		reconnect.MaxAttempts = 1
		if err := connections.Connect(ctx, reconnect, logger); err != nil {
			return fmt.Errorf("connect to temporal: %w", err)
		}
		logger.Info("dry run: config, api spec, and temporal connections are all valid; exiting without starting the server")
		return nil
	}

	// Readiness reports each namespace as "not connected yet" until it's
	// dialed, then its live health (ADR-019, ADR-026).
	probes := health.NewProbes("starting")
	probes.Ready(connections.CheckHealth)
	if cfg.Health.Enabled {
		healthServer, err := startHealthServer(fmt.Sprintf("%s:%d", cfg.Health.Host, cfg.Health.Port), probes.Handler(), logger)
		if err != nil {
			return fmt.Errorf("start health server: %w", err)
		}
		// Close only fails if closing the listener does; nothing to do then.
		defer func() { _ = healthServer.Close() }()
	}

	dispatcher := temporal.NewDispatcher(connections)
	requestTimeout := cfg.Server.RequestTimeoutDuration()
	handler := gateway.NewHandler(apiSpec, dispatcher, gateway.Options{
		MaxBodyBytes:   cfg.Server.MaxBodyBytesOrDefault(),
		RequestTimeout: requestTimeout,
	}, logger)
	server := newServer(fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port), handler, requestTimeout)
	// serve has already shut server down gracefully; this only releases
	// what's left, and has nothing actionable to report.
	defer func() { _ = server.Close() }()

	// The API serves right away; Temporal is dialed in the background, and
	// each namespace's routes answer 503 until it connects (ADR-026). A dial
	// that gives up (temporal.reconnect.maxAttempts) stops the gateway.
	serveCtx, stopServing := context.WithCancel(ctx)
	defer stopServing()
	connectErr := make(chan error, 1)
	go func() {
		err := connections.Connect(serveCtx, cfg.Temporal.Reconnect, logger)
		if err != nil {
			stopServing()
		}
		connectErr <- err
	}()

	serveErr := serve(serveCtx, server, probes, logger)
	stopServing()
	// Wait for Connect to return, so a dial finishing during shutdown is
	// still closed by the deferred connections.Close.
	dialErr := <-connectErr
	if serveErr != nil {
		return serveErr
	}
	if dialErr != nil && ctx.Err() == nil {
		return fmt.Errorf("connect to temporal: %w", dialErr)
	}
	return nil
}

// cliFlags holds the process's parsed command-line flags.
type cliFlags struct {
	configPath string
	envPath    string
	dryRun     bool
}

// parseFlags parses args (the command line minus the program name).
// -h/-help returns flag.ErrHelp after printing usage.
func parseFlags(args []string) (cliFlags, error) {
	fs := flag.NewFlagSet("temporal-gateway", flag.ContinueOnError)
	configPath := fs.String("config", "config.yml", "path to the gateway config file")
	envPath := fs.String("env", ".env", "path to a .env file with environment variables (missing file is not an error)")
	dryRun := fs.Bool("dry-run", false, "load and validate config, api spec, and temporal connections (a single dial attempt, no reconnect), then exit without starting the server")
	if err := fs.Parse(args); err != nil {
		return cliFlags{}, err
	}
	return cliFlags{configPath: *configPath, envPath: *envPath, dryRun: *dryRun}, nil
}

// logStartup records what was loaded: the resolved config, the API spec, and
// every route/workflow the gateway will serve. It's kept separate from run's
// control flow so this purely informational logging can grow without
// touching the wiring logic around it.
func logStartup(logger *slog.Logger, configPath string, specPaths []string, cfg *config.GatewayConfig, apiSpec *spec.Spec) {
	middlewares := make([]any, len(cfg.Middlewares))
	for i, mw := range cfg.Middlewares {
		middlewares[i] = map[string]any{"name": mw.Name, "enabled": mw.Enabled}
	}

	namespaces := make([]string, len(cfg.Temporal.Connections))
	for i, c := range cfg.Temporal.Connections {
		namespaces[i] = c.Namespace
	}

	logger.Info("loaded gateway config",
		"config_path", configPath,
		slog.Group("server", "host", cfg.Server.Host, "port", cfg.Server.Port),
		"temporal_namespaces", namespaces,
		slog.Group("temporal_reconnect", "interval", cfg.Temporal.Reconnect.IntervalDuration().String(), "max_attempts", cfg.Temporal.Reconnect.MaxAttempts),
		"auth_type", cfg.Auth.Type,
		"middlewares", middlewares,
	)

	logger.Info("loaded api spec",
		"spec_paths", specPaths,
		"title", apiSpec.Info.Title,
		"version", apiSpec.Info.Version,
	)

	for _, route := range apiSpec.Routes() {
		triggers := route.Operation.Temporal.Triggers
		actions := make([]string, len(triggers))
		bindingNamespaces := make([]string, len(triggers))
		for i, t := range triggers {
			actions[i] = string(t.Action)
			bindingNamespaces[i] = t.Namespace
		}
		logger.Info("route registered",
			"method", route.Method,
			"path", route.Path,
			"operation_id", route.Operation.OperationID,
			"return_strategy", route.Operation.Temporal.Strategy(),
			"temporal_actions", actions,
			"temporal_namespaces", bindingNamespaces,
		)
	}

	for _, conn := range cfg.Temporal.Connections {
		for _, wf := range conn.Workflows {
			logger.Info("workflow registered",
				"namespace", conn.Namespace,
				"workflow_type", wf.Name,
				"task_queue", wf.TaskQueue,
				"signals", wf.Signals,
				"queries", wf.Queries,
			)
		}
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

// serve runs server until ctx is cancelled (SIGINT/SIGTERM), then marks the
// gateway not ready and shuts server down gracefully, so in-flight requests
// are drained instead of dropped, within shutdownTimeout.
func serve(ctx context.Context, server *http.Server, probes *health.Probes, logger *slog.Logger) error {
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
	// to finish, or run's deferred cleanup would cut in-flight requests off.
	<-shutdownDone
	return nil
}
