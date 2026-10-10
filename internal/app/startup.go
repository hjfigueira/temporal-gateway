package app

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"

	"github.com/joho/godotenv"

	"temporal-gateway/internal/config"
	"temporal-gateway/internal/spec"
)

// ParseFlags parses State.Args (the command line minus the program name)
// into State.Flags. -h/-help prints usage and ends the program.
func ParseFlags() Module {
	return Module{Init: func(_ context.Context, s *State) error {
		set := flag.NewFlagSet("temporal-gateway", flag.ContinueOnError)
		configPath := set.String("config", "config.yml", "path to the gateway config file")
		envPath := set.String("env", ".env", "path to a .env file with environment variables (missing file is not an error)")
		dryRun := set.Bool("dry-run", false, "load and validate config, api spec, and temporal connections (a single dial attempt, no reconnect), then exit without starting the server")
		err := set.Parse(s.Args)
		if errors.Is(err, flag.ErrHelp) {
			return ErrDone
		}
		if err != nil {
			return err
		}
		s.Flags = Flags{ConfigPath: *configPath, EnvPath: *envPath, DryRun: *dryRun}
		return nil
	}}
}

// LoadDotEnv loads the -env file into the environment. A missing .env is
// normal: most deployments set real environment variables instead.
// Variables already set always win over the file.
func LoadDotEnv() Module {
	return Module{Init: func(_ context.Context, s *State) error {
		if err := godotenv.Load(s.Flags.EnvPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("load env file %q: %w", s.Flags.EnvPath, err)
		}
		return nil
	}}
}

// LoadConfig loads config.yml into State.Config.
func LoadConfig() Module {
	return Module{Init: func(_ context.Context, s *State) error {
		cfg, err := config.Load(s.Flags.ConfigPath)
		if err != nil {
			return fmt.Errorf("load gateway config %q: %w", s.Flags.ConfigPath, err)
		}
		s.Config = cfg
		return nil
	}}
}

// Telemetry configures tracing with setup (telemetry.Setup), and flushes it
// on Stop. It must come before
// the Temporal and API modules, so both pick up the real TracerProvider from
// the start rather than the no-op one otel installs by default.
func Telemetry(setup func(context.Context, config.OTelConfig) (func(context.Context) error, error)) Module {
	var shutdown func(context.Context) error
	return Module{
		Init: func(_ context.Context, s *State) error {
			var err error
			if shutdown, err = setup(context.Background(), s.Config.OTel); err != nil {
				return fmt.Errorf("set up telemetry: %w", err)
			}
			s.Logger.Info("configured telemetry", "otel_enabled", s.Config.OTel.Enabled, "otel_endpoint", s.Config.OTel.Endpoint)
			return nil
		},
		Stop: func(ctx context.Context, s *State) {
			if err := shutdown(ctx); err != nil {
				s.Logger.Error("failed to shut down telemetry", "error", err)
			}
		},
	}
}

// LoadSpec loads the API spec files config.yml points at into State.Spec.
func LoadSpec() Module {
	return Module{Init: func(_ context.Context, s *State) error {
		s.SpecPaths = s.Config.ResolveSpecPaths()
		apiSpec, err := spec.Load(s.SpecPaths...)
		if err != nil {
			return fmt.Errorf("load api spec %v: %w", s.SpecPaths, err)
		}
		s.Spec = apiSpec
		return nil
	}}
}

// LogStartup records what was loaded: the resolved config, the API spec,
// and every route/workflow the gateway will serve. Purely informational.
func LogStartup() Module {
	return Module{Init: func(_ context.Context, s *State) error {
		logStartup(s.Logger, s.Flags.ConfigPath, s.SpecPaths, s.Config, s.Spec)
		return nil
	}}
}

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
