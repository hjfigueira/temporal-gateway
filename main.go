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
	"temporal-gateway/internal/gateway"
	"temporal-gateway/internal/spec"
	"temporal-gateway/internal/temporal"
)

// shutdownTimeout bounds how long the server waits for in-flight requests to
// finish once a shutdown signal is received.
const shutdownTimeout = 10 * time.Second

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	configPath := flag.String("config", "config.yml", "path to the gateway config file")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		logger.Error("failed to load gateway config", "config_path", *configPath, "error", err)
		os.Exit(1)
	}

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

	logger.Info("loaded gateway config",
		"config_path", *configPath,
		slog.Group("server", "host", cfg.Server.Host, "port", cfg.Server.Port),
		slog.Group("temporal", "host_port", cfg.Temporal.HostPort, "namespace", cfg.Temporal.Namespace),
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
		for i, t := range route.Operation.Temporal {
			actions[i] = string(t.Action)
		}
		logger.Info("route registered",
			"method", route.Method,
			"path", route.Path,
			"operation_id", route.Operation.OperationID,
			"temporal_actions", actions,
		)
	}

	for _, wf := range cfg.Temporal.Workflows {
		logger.Info("workflow registered",
			"workflow_type", wf.Name,
			"task_queue", wf.TaskQueue,
			"signals", wf.Signals,
			"queries", wf.Queries,
		)
	}

	catalog := temporal.NewCatalog(cfg.Temporal.Workflows)

	temporalClient, err := temporal.NewClient(cfg.Temporal)
	if err != nil {
		logger.Error("failed to connect to temporal", "error", err)
		os.Exit(1)
	}
	defer temporalClient.Close()

	dispatcher := temporal.NewDispatcher(temporalClient, catalog)
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
		temporalClient.Close()
		os.Exit(1)
	}
}
