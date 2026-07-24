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
// finish once a shutdown signal is received.
const shutdownTimeout = 10 * time.Second

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	configPath := flag.String("config", "config.yml", "path to the gateway config file")
	envPath := flag.String("env", ".env", "path to a .env file with environment variables (missing file is not an error)")
	flag.Parse()

	if err := dotenv.Load(*envPath); err != nil {
		logger.Error("failed to load env file", "env_path", *envPath, "error", err)
		os.Exit(1)
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		logger.Error("failed to load gateway config", "config_path", *configPath, "error", err)
		os.Exit(1)
	}

	// Configure tracing before dialing Temporal or building the HTTP
	// handler, so both pick up the real TracerProvider from the start
	// rather than the no-op one otel installs by default.
	shutdownTelemetry, err := telemetry.Setup(context.Background(), cfg.OTel)
	if err != nil {
		logger.Error("failed to set up telemetry", "error", err)
		os.Exit(1)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := shutdownTelemetry(shutdownCtx); err != nil {
			logger.Error("failed to shut down telemetry", "error", err)
		}
	}()
	logger.Info("configured telemetry",
		"otel_enabled", cfg.OTel.Enabled,
		"otel_endpoint", cfg.OTel.Endpoint,
	)

	specPath := cfg.ResolveSpecPath()
	apiSpec, err := spec.Load(specPath)
	if err != nil {
		logger.Error("failed to load api spec", "spec_path", specPath, "error", err)
		os.Exit(1)
	}

	middlewares := make([]any, len(cfg.Middlewares))
	for i, mw := range cfg.Middlewares {
		middlewares[i] = map[string]any{"name": mw.Name, "enabled": mw.Enabled}
	}

	namespaces := make([]string, len(cfg.Temporal.Connections))
	for i, c := range cfg.Temporal.Connections {
		namespaces[i] = c.Namespace
	}

	logger.Info("loaded gateway config",
		"config_path", *configPath,
		slog.Group("server", "host", cfg.Server.Host, "port", cfg.Server.Port),
		"temporal_namespaces", namespaces,
		"auth_type", cfg.Auth.Type,
		"middlewares", middlewares,
	)

	logger.Info("loaded api spec",
		"spec_path", specPath,
		"title", apiSpec.Info.Title,
		"version", apiSpec.Info.Version,
	)

	for _, route := range apiSpec.Routes() {
		actions := make([]string, len(route.Operation.Temporal))
		bindingNamespaces := make([]string, len(route.Operation.Temporal))
		for i, t := range route.Operation.Temporal {
			actions[i] = string(t.Action)
			bindingNamespaces[i] = t.Namespace
		}
		logger.Info("route registered",
			"method", route.Method,
			"path", route.Path,
			"operation_id", route.Operation.OperationID,
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

	connections, err := temporal.NewConnections(cfg.Temporal)
	if err != nil {
		// NewConnections returns whatever it managed to dial before the
		// failure (never nil), so this closes those rather than leaking
		// them.
		if connections != nil {
			connections.Close()
		}
		logger.Error("failed to connect to temporal", "error", err)
		os.Exit(1)
	}
	defer connections.Close()

	if err := temporal.ValidateNamespaces(apiSpec, connections); err != nil {
		logger.Error("api spec references unknown temporal namespace", "error", err)
		os.Exit(1)
	}

	dispatcher := temporal.NewDispatcher(connections)
	handler := gateway.NewHandler(apiSpec, dispatcher, logger)
	addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)

	server := &http.Server{
		Addr:    addr,
		Handler: handler,
		// ReadHeaderTimeout bounds how long a client may take sending
		// request headers, so a slow/stalled client can't tie up a
		// connection indefinitely (a "slowloris" resource-exhaustion risk
		// with the net/http default of no timeout).
		ReadHeaderTimeout: 10 * time.Second,
	}

	// Shut down on SIGINT/SIGTERM by draining in-flight requests instead of
	// dropping them, and closing the Temporal connection cleanly - without
	// this, the OS kills the process directly and none of that happens.
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

	logger.Info("starting http server", "addr", addr)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("http server stopped", "error", err)
		connections.Close()
		os.Exit(1)
	}
}
