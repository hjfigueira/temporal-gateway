// Package config loads the main gateway configuration: server settings,
// the Temporal connection, authentication, middlewares, and the path to the
// API specification that drives route generation. Before parsing, the raw
// file is run through envsubst.Expand, so values may reference "${VAR}" or
// "${VAR:-default}" environment variables.
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

type TemporalTLSConfig struct {
	Enabled  bool   `yaml:"enabled"`
	CertPath string `yaml:"certPath,omitempty"`
	KeyPath  string `yaml:"keyPath,omitempty"`
}

// WorkflowDefinition documents a workflow type the gateway addresses: its
// default task queue (used when an x-temporal binding doesn't set its own
// taskQueue) and, informationally, the signals/queries it exposes. It is not
// cross-checked against the API spec's x-temporal bindings; an unknown
// workflow, signal, or query surfaces as a Temporal error at request time.
type WorkflowDefinition struct {
	Name      string   `yaml:"name"`
	TaskQueue string   `yaml:"taskQueue"`
	Signals   []string `yaml:"signals,omitempty"`
	Queries   []string `yaml:"queries,omitempty"`
}

type TemporalConfig struct {
	HostPort  string               `yaml:"hostPort"`
	Namespace string               `yaml:"namespace"`
	TLS       TemporalTLSConfig    `yaml:"tls"`
	Workflows []WorkflowDefinition `yaml:"workflows"`
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

// GatewayConfig is the root of the main configuration file. It links to an
// API specification (APISpec) that defines the actual HTTP surface.
type GatewayConfig struct {
	Server      ServerConfig       `yaml:"server"`
	Temporal    TemporalConfig     `yaml:"temporal"`
	APISpec     string             `yaml:"apiSpec"`
	Auth        AuthConfig         `yaml:"auth"`
	Middlewares []MiddlewareConfig `yaml:"middlewares"`

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

	if cfg.APISpec == "" {
		return nil, fmt.Errorf("config: %q: apiSpec is required", absPath)
	}

	cfg.baseDir = filepath.Dir(absPath)
	return &cfg, nil
}

// ResolveSpecPath returns the absolute path to the API specification,
// resolving it relative to the directory containing the loaded config file
// when APISpec is not already absolute.
func (c *GatewayConfig) ResolveSpecPath() string {
	if filepath.IsAbs(c.APISpec) {
		return c.APISpec
	}
	return filepath.Join(c.baseDir, c.APISpec)
}
