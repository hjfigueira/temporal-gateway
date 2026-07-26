// Package config loads the main gateway configuration: server settings,
// the Temporal connection, authentication, middlewares, and the path(s) to
// the API specification(s) that drive route generation. Before parsing, the
// raw file is run through envsubst.Expand, so values may reference "${VAR}"
// or "${VAR:-default}" environment variables.
package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"temporal-gateway/internal/envsubst"
)

type ServerConfig struct {
	Host string `yaml:"host"`
	Port int    `yaml:"port"`
}

type APIKeyAuthConfig struct {
	Header string   `yaml:"header"`
	Keys   []string `yaml:"keys"`
}

type JWTAuthConfig struct {
	Issuer   string `yaml:"issuer"`
	Audience string `yaml:"audience"`
	JWKSURL  string `yaml:"jwksUrl"`
}

// AuthConfig configures how incoming requests are authenticated: Type
// selects which of the nested configs applies ("none", "apiKey", or "jwt").
// It is currently only parsed and logged at startup - the gateway does not
// yet enforce it against incoming requests.
type AuthConfig struct {
	Type   string            `yaml:"type"`
	APIKey *APIKeyAuthConfig `yaml:"apiKey,omitempty"`
	JWT    *JWTAuthConfig    `yaml:"jwt,omitempty"`
}

// MiddlewareConfig enables and configures a named middleware (e.g. logging,
// cors, rateLimit); Config is free-form so each middleware can define its
// own options without changing this struct. It is currently only parsed and
// logged at startup - the gateway does not yet apply any middleware to the
// request pipeline.
type MiddlewareConfig struct {
	Name    string         `yaml:"name"`
	Enabled bool           `yaml:"enabled"`
	Config  map[string]any `yaml:"config,omitempty"`
}

// OTelConfig configures OpenTelemetry distributed tracing (see
// internal/telemetry.Setup). When Enabled, the gateway starts one span per
// HTTP request and exports it via OTLP/gRPC to Endpoint; that span's trace
// context is also propagated into the Temporal headers of every workflow
// action the request dispatches (see internal/temporal.NewClient), so a
// workflow's own tracing, if instrumented, continues the same trace.
type OTelConfig struct {
	Enabled bool `yaml:"enabled"`
	// ServiceName identifies this process in the exported spans' resource
	// attributes (the OpenTelemetry "service.name").
	ServiceName string `yaml:"serviceName"`
	// Endpoint is the OTLP/gRPC collector address, e.g. "localhost:4317".
	Endpoint string `yaml:"endpoint"`
	// Insecure disables TLS on the connection to Endpoint (typical for a
	// collector running as a local/sidecar process).
	Insecure bool `yaml:"insecure"`
	// SampleRatio is the fraction of traces to sample, from 0 (none) to 1
	// (all). Values <= 0 are treated as 1 (sample everything).
	SampleRatio float64 `yaml:"sampleRatio"`
}

// GatewayConfig is the root of the main configuration file. It links to one
// or more API specifications (APISpec) that define the actual HTTP surface.
type GatewayConfig struct {
	Server      ServerConfig       `yaml:"server"`
	Temporal    TemporalConfig     `yaml:"temporal"`
	APISpec     APISpecPaths       `yaml:"apiSpec"`
	Auth        AuthConfig         `yaml:"auth"`
	Middlewares []MiddlewareConfig `yaml:"middlewares"`
	OTel        OTelConfig         `yaml:"otel"`

	// baseDir is the directory containing the config file, used to resolve
	// a relative APISpec path regardless of the process's working directory.
	baseDir string
}

// Load reads and parses a GatewayConfig from path.
func Load(path string) (*GatewayConfig, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("config: resolve path %q: %w", path, err)
	}

	data, err := os.ReadFile(absPath)
	if err != nil {
		return nil, fmt.Errorf("config: read %q: %w", absPath, err)
	}

	data, err = envsubst.Expand(data)
	if err != nil {
		return nil, fmt.Errorf("config: %q: %w", absPath, err)
	}

	var cfg GatewayConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("config: parse %q: %w", absPath, err)
	}

	if err := cfg.APISpec.validate(); err != nil {
		return nil, fmt.Errorf("config: %q: %w", absPath, err)
	}

	if err := cfg.Temporal.validate(); err != nil {
		return nil, fmt.Errorf("config: %q: %w", absPath, err)
	}

	cfg.baseDir = filepath.Dir(absPath)
	return &cfg, nil
}

// ResolveSpecPaths returns the absolute paths to every configured API
// specification, in the order they should be merged (see spec.Load), each
// resolved relative to the directory containing the loaded config file when
// not already absolute.
func (c *GatewayConfig) ResolveSpecPaths() []string {
	paths := make([]string, len(c.APISpec))
	for i, p := range c.APISpec {
		if filepath.IsAbs(p) {
			paths[i] = p
			continue
		}
		paths[i] = filepath.Join(c.baseDir, p)
	}
	return paths
}
