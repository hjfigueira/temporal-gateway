package app

import (
	"context"
	"fmt"

	"temporal-gateway/internal/temporal"
)

// Temporal builds the namespace connection table into State.Connections,
// dials every namespace with dial (temporal.NewClient) in the background
// while the API serves (ADR-026), and closes every dialed client on Stop. A dial that gives up
// (temporal.reconnect.maxAttempts) stops the gateway.
func Temporal(dial temporal.DialFunc) Module {
	return Module{
		Init: func(_ context.Context, s *State) error {
			s.Connections = temporal.NewConnections(s.Config.Temporal, dial)
			// Needs only config.yml (namespaces, workflow catalogs), so a bad
			// reference fails startup even while Temporal is unreachable.
			if err := temporal.ValidateBindings(s.Spec, s.Connections); err != nil {
				return fmt.Errorf("api spec doesn't match temporal config: %w", err)
			}
			return nil
		},
		Run: func(ctx context.Context, s *State) error {
			if err := s.Connections.Connect(ctx, s.Config.Temporal.Reconnect, s.Logger); err != nil {
				return fmt.Errorf("connect to temporal: %w", err)
			}
			return nil
		},
		// Run has returned by now, so a dial finishing during shutdown is
		// still closed here.
		Stop: func(_ context.Context, s *State) { s.Connections.Close() },
	}
}

// DryRun, under --dry-run, dials Temporal now, once, instead of serving
// while retrying, then ends the program: a pass/fail check for CI or
// pre-deploy.
func DryRun() Module {
	return Module{Init: func(ctx context.Context, s *State) error {
		if !s.Flags.DryRun {
			return nil
		}
		reconnect := s.Config.Temporal.Reconnect
		reconnect.MaxAttempts = 1
		if err := s.Connections.Connect(ctx, reconnect, s.Logger); err != nil {
			return fmt.Errorf("connect to temporal: %w", err)
		}
		s.Logger.Info("dry run: config, api spec, and temporal connections are all valid; exiting without starting the server")
		return ErrDone
	}}
}
