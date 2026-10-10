package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const validTemporal = `
temporal:
  connections:
    - namespace: default
      host: localhost:7233
`

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadResolvesSpecPaths(t *testing.T) {
	path := writeConfig(t, `apiSpec: [./base.yaml, /abs/override.yaml]`+validTemporal)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := []string{filepath.Join(filepath.Dir(path), "base.yaml"), "/abs/override.yaml"}
	if got := cfg.ResolveSpecPaths(); !reflect.DeepEqual(got, want) {
		t.Fatalf("ResolveSpecPaths() = %v, want %v", got, want)
	}
}

func TestLoadAcceptsSingleSpecPath(t *testing.T) {
	cfg, err := Load(writeConfig(t, `apiSpec: ./api-spec.yaml`+validTemporal))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !reflect.DeepEqual(cfg.APISpec, APISpecPaths{"./api-spec.yaml"}) {
		t.Fatalf("APISpec = %v", cfg.APISpec)
	}
}

func TestLoadErrors(t *testing.T) {
	t.Setenv("UNSET_FOR_TEST", "")
	_ = os.Unsetenv("UNSET_FOR_TEST")

	tests := []struct {
		name    string
		body    string
		wantErr string
	}{
		{name: "unset env var", body: `apiSpec: "${UNSET_FOR_TEST}"`, wantErr: "UNSET_FOR_TEST"},
		{name: "malformed yaml", body: "apiSpec: [", wantErr: "parse"},
		{name: "apiSpec not a string or list", body: "apiSpec: {a: b}" + validTemporal, wantErr: "must be a string or a list"},
		{name: "missing apiSpec", body: validTemporal, wantErr: "apiSpec is required"},
		{name: "blank apiSpec entry", body: `apiSpec: ["./a.yaml", ""]` + validTemporal, wantErr: "apiSpec[1] is empty"},
		{name: "bad server", body: "server: {maxBodyBytes: -1}\napiSpec: a.yaml" + validTemporal, wantErr: "server.maxBodyBytes"},
		{name: "bad temporal", body: "apiSpec: a.yaml", wantErr: "temporal.connections requires"},
		{name: "bad health", body: "health: {enabled: true, port: 0}\napiSpec: a.yaml" + validTemporal, wantErr: "health.port"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load(writeConfig(t, tt.body))
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Load err = %v, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

func TestLoadMissingFile(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "nope.yml")); err == nil || !strings.Contains(err.Error(), "read") {
		t.Fatalf("Load err = %v, want a read error", err)
	}
}

func TestLoadUnresolvableRelativePath(t *testing.T) {
	// filepath.Abs on a relative path fails once the working directory no
	// longer exists.
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.Remove(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := Load("config.yml"); err == nil || !strings.Contains(err.Error(), "resolve path") {
		t.Fatalf("Load err = %v, want a resolve-path error", err)
	}
}

func TestTemporalConfigValidate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     TemporalConfig
		wantErr string
	}{
		{name: "bad reconnect", cfg: TemporalConfig{Reconnect: TemporalReconnectConfig{MaxAttempts: -1}}, wantErr: "maxAttempts"},
		{name: "missing namespace", cfg: TemporalConfig{Connections: []TemporalConnectionConfig{{}}}, wantErr: "missing namespace"},
		{name: "duplicate namespace", cfg: TemporalConfig{Connections: []TemporalConnectionConfig{{Namespace: "a"}, {Namespace: "a"}}}, wantErr: "duplicate namespace"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.cfg.validate(); err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("validate() err = %v, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}
