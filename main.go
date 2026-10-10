// Command temporal-gateway reads a gateway config and its linked API
// specification, then generates HTTP endpoints in front of Temporal.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"temporal-gateway/internal/app"
	"temporal-gateway/internal/telemetry"
	"temporal-gateway/internal/temporal"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	// Cancelled on SIGINT/SIGTERM: interrupts a startup that's still
	// waiting for Temporal to come up, and triggers the HTTP server's
	// graceful shutdown once it's serving.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)

	// Order matters: each module reads what the ones before it put on the
	// State, and they stop in reverse.
	err := app.Run(ctx, &app.State{Args: os.Args[1:], Logger: logger, ShutdownTimeout: 10 * time.Second},
		app.ParseFlags(),
		app.LoadDotEnv(),
		app.LoadConfig(),
		app.Telemetry(telemetry.Setup),
		app.LoadSpec(),
		app.LogStartup(),
		app.Temporal(temporal.NewClient),
		app.DryRun(),
		app.Health(),
		app.API(),
	)
	stop()
	if err != nil {
		logger.Error("fatal", "error", err)
		os.Exit(1)
	}
}
