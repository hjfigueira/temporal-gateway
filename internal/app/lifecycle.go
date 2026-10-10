// Package app is the gateway's composition root: a small lifecycle engine
// (Module, Run) plus one Module per stage of the program (flags, config,
// telemetry, spec, Temporal, probes, API). main lists the modules in order
// and hands them to Run; the domain packages under internal/ never depend on
// this one.
package app

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"temporal-gateway/internal/config"
	"temporal-gateway/internal/gateway/health"
	"temporal-gateway/internal/spec"
	"temporal-gateway/internal/temporal"
)

// ErrDone, returned by an Init hook, ends the program successfully without
// running the later modules (-h, --dry-run). Stop hooks still run.
var ErrDone = errors.New("done")

// State is the data modules pass to each other: main seeds Args, Logger and
// ShutdownTimeout, and each module's Init fills in what it owns for the
// modules after it.
type State struct {
	Args   []string
	Logger *slog.Logger
	// ShutdownTimeout bounds each Stop hook, and how long the API server
	// waits for in-flight requests to finish once shutdown begins.
	ShutdownTimeout time.Duration

	Flags       Flags
	Config      *config.GatewayConfig
	SpecPaths   []string
	Spec        *spec.Spec
	Connections temporal.Connections
	Probes      *health.Probes
}

// Flags holds the process's parsed command-line flags.
type Flags struct {
	ConfigPath string
	EnvPath    string
	DryRun     bool
}

// Module is one stage of the program's lifecycle. Every hook is optional.
type Module struct {
	// Init runs in module order, before any Run. A module builds what it
	// owns here and stores it on the State for the modules after it.
	Init func(ctx context.Context, s *State) error
	// Run runs concurrently with every other module's Run once all Inits
	// succeeded, and must return once ctx is done. An error cancels ctx for
	// the others and becomes Run's result; a nil return stops nothing.
	Run func(ctx context.Context, s *State) error
	// Stop releases what Init acquired. It runs in reverse module order for
	// every module whose Init succeeded, whichever later stage failed, with
	// a State.ShutdownTimeout deadline.
	Stop func(ctx context.Context, s *State)
}

// Run drives modules through Init, Run and Stop. Every stage returns an
// error instead of exiting, so Stop hooks (closing Temporal connections,
// flushing telemetry) always run - os.Exit skips defers, which is why main
// calls it only after Run has returned and unwound.
func Run(ctx context.Context, s *State, modules ...Module) error {
	var started []Module
	defer func() { stopAll(s, started) }()

	for _, m := range modules {
		var err error
		if m.Init != nil {
			err = m.Init(ctx, s)
		}
		if errors.Is(err, ErrDone) {
			started = append(started, m)
			return nil
		}
		if err != nil {
			return err
		}
		started = append(started, m)
	}
	return runAll(ctx, s, started)
}

func runAll(ctx context.Context, s *State, modules []Module) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var (
		wg    sync.WaitGroup
		mu    sync.Mutex
		first error
	)
	for _, m := range modules {
		if m.Run == nil {
			continue
		}
		wg.Go(func() {
			err := m.Run(ctx, s)
			if err == nil {
				return
			}
			mu.Lock()
			defer mu.Unlock()
			// Errors once shutdown began (signal or an earlier failure) are
			// its fallout, not its cause.
			if ctx.Err() == nil {
				first = err
				cancel()
			}
		})
	}
	wg.Wait()
	return first
}

func stopAll(s *State, modules []Module) {
	for i := len(modules) - 1; i >= 0; i-- {
		if stop := modules[i].Stop; stop != nil {
			ctx, cancel := context.WithTimeout(context.Background(), s.ShutdownTimeout)
			stop(ctx, s)
			cancel()
		}
	}
}
