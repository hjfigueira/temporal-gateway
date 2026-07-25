// Command temporal-gateway reads a gateway config and its linked API
// specification, then generates HTTP endpoints in front of Temporal.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"temporal-gateway/internal/config"
	"temporal-gateway/internal/dotenv"
	"temporal-gateway/internal/gateway"
	"temporal-gateway/internal/spec"
	"temporal-gateway/internal/telemetry"
	"temporal-gateway/internal/temporal"
)

// shutdownTimeout bounds how long the server waits for in-flight requests to
// finish once a shutdown signal is received, and how long telemetry export
// gets to flush on the same shutdown.
const shutdownTimeout = 10 * time.Second

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	if err := run(logger); err != nil {
		logger.Error("fatal", "error", err)
		os.Exit(1)
	}
}

// run wires the gateway's dependencies and serves until a shutdown signal
// arrives. Every stage returns an error instead of exiting directly, so
// deferred cleanup (closing Temporal connections, flushing telemetry) always
// runs - os.Exit skips defers, which is why it's called exactly once, in
// main, after run has already returned and unwound.
func run(logger *slog.Logger) error {
	flags := parseFlags()

	if err := dotenv.Load(flags.envPath); err != nil {
		return fmt.Errorf("load env file %q: %w", flags.envPath, err)
	}

	cfg, err := config.Load(flags.configPath)
	if err != nil {
		return fmt.Errorf("load gateway config %q: %w", flags.configPath, err)
	}

	// Configure tracing before dialing Temporal or building the HTTP
	// handler, so both pick up the real TracerProvider from the start
	// rather than the no-op one otel installs by default.
	shutdownTelemetry, err := telemetry.Setup(context.Background(), cfg.OTel)
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

	connections, err := temporal.NewConnections(cfg.Temporal)
	if err != nil {
		// NewConnections returns whatever it managed to dial before the
		// failure (never nil), so this closes those rather than leaking
		// them.
		if connections != nil {
			connections.Close()
		}
		return fmt.Errorf("connect to temporal: %w", err)
	}
	defer connections.Close()

	if err := temporal.ValidateNamespaces(apiSpec, connections); err != nil {
		return fmt.Errorf("api spec references unknown temporal namespace: %w", err)
	}

	if flags.dryRun {
		logger.Info("dry run: config, api spec, and temporal connections are all valid; exiting without starting the server")
		return nil
	}

	dispatcher := temporal.NewDispatcher(connections)
	handler := gateway.NewHandler(apiSpec, dispatcher, logger)
	server := newServer(fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port), handler)
	defer func(server *http.Server) {
		err := server.Close()
		if err != nil {
			logger.Error("failed to close server", "err", err)
		}
	}(server)
	return serve(server, logger)
}

// cliFlags holds the process's parsed command-line flags.
type cliFlags struct {
	configPath string
	envPath    string
	dryRun     bool
}

// parseFlags reads the process's command-line flags.
func parseFlags() cliFlags {
	configPath := flag.String("config", "config.yml", "path to the gateway config file")
	envPath := flag.String("env", ".env", "path to a .env file with environment variables (missing file is not an error)")
	dryRun := flag.Bool("dry-run", false, "load and validate config, api spec, and temporal connections, then exit without starting the server")
	flag.Parse()
	return cliFlags{configPath: *configPath, envPath: *envPath, dryRun: *dryRun}
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
// how long a client may hold a connection open at each stage.
func newServer(addr string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:    addr,
		Handler: handler,
		// ReadHeaderTimeout bounds how long a client may take sending
		// request headers, so a slow/stalled client can't tie up a
		// connection indefinitely (a "slowloris" resource-exhaustion risk
		// with the net/http default of no timeout).
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		// IdleTimeout closes keep-alive connections that go quiet instead
		// of holding their read/write buffers open indefinitely.
		IdleTimeout: 60 * time.Second,
	}
}

// serve runs server until a SIGINT/SIGTERM triggers a graceful shutdown, so
// in-flight requests are drained instead of dropped, within shutdownTimeout.
func serve(server *http.Server, logger *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		<-ctx.Done()
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
	return nil
}
