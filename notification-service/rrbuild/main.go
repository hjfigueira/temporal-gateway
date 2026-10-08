// Command rr-custom is notification-service's own RoadRunner build: the
// stock plugins its .rr.yaml already used (config, logger, rpc, server,
// http, temporal) plus cascadefilter, a plugin that exists nowhere in
// RoadRunner's own prebuilt binaries - see cascadefilter's package doc for
// why it has to be a separate plugin rather than a fork of the stock
// "temporal" one. RoadRunner's own official binary is built the same way
// (see github.com/roadrunner-server/roadrunner's container.Plugins() and
// cmd/rr/main.go), just with a much larger stock plugin list and its own
// serve/stop/jobs/workers/reset CLI surface (internal/cli, unexported - not
// importable from here) - this only replicates the "serve" path, since
// running (and holding open) notification-service's worker/HTTP processes
// is the only thing this binary needs to do.
package main

import (
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/roadrunner-server/endure/v2"

	configImpl "github.com/roadrunner-server/config/v5"
	httpPlugin "github.com/roadrunner-server/http/v5"
	"github.com/roadrunner-server/informer/v5"
	"github.com/roadrunner-server/logger/v5"
	"github.com/roadrunner-server/resetter/v5"
	rpcPlugin "github.com/roadrunner-server/rpc/v5"
	"github.com/roadrunner-server/server/v5"
	rrtemporal "github.com/temporalio/roadrunner-temporal/v5"

	"notification-service/rrbuild/cascadefilter"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	cfgFile := ".rr.yaml"
	for i, arg := range os.Args {
		if (arg == "-c" || arg == "--config") && i+1 < len(os.Args) {
			cfgFile = os.Args[i+1]
		}
	}

	// Version must be a real semver RR release string, not an arbitrary
	// label - found by hand: the PHP side parses it with composer/semver
	// (VersionParser), which throws on anything else. This matches the
	// stock ./rr binary notification-service used before this build
	// existed, so PHP-side version gating behaves identically.
	cfg := &configImpl.Plugin{Path: cfgFile, Version: "2025.1.15"}

	cont := endure.New(slog.LevelInfo)

	// Same list .rr.yaml already exercised via the stock ./rr binary
	// (temporal, http, rpc - server is the shared dependency both temporal
	// and http use to spawn their PHP worker pools; informer/resetter back
	// the `rr workers`/`rr reset` RPC calls other tooling may still expect)
	// plus cascadefilter, the one addition this build exists for.
	plugins := []any{
		cfg,
		&logger.Plugin{},
		&rpcPlugin.Plugin{},
		&server.Plugin{},
		&informer.Plugin{},
		&resetter.Plugin{},
		&httpPlugin.Plugin{},
		&rrtemporal.Plugin{},
		&cascadefilter.Plugin{},
	}

	if err := cont.RegisterAll(plugins...); err != nil {
		return fmt.Errorf("register plugins: %w", err)
	}
	if err := cont.Init(); err != nil {
		return fmt.Errorf("init container: %w", err)
	}

	errCh, err := cont.Serve()
	if err != nil {
		return fmt.Errorf("serve container: %w", err)
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)

	fmt.Println("[INFO] rr-custom started")

	select {
	case e := <-errCh:
		return fmt.Errorf("plugin %s: %w", e.VertexID, e.Error)
	case <-sig:
		fmt.Println("[INFO] stop signal received, shutting down")
		return cont.Stop()
	}
}
