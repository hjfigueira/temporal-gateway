package config

import (
	"os"
	"path/filepath"
	"testing"
)

// configHeader is a minimal valid GatewayConfig document sans apiSpec; tests
// append their own "apiSpec: ..." line to exercise scalar vs list forms.
const configHeader = `server:
  host: "0.0.0.0"
  port: 8081
temporal:
  connections:
    - namespace: default
      host: localhost:7233
`

// loadConfig writes yamlContent to a temp file under dir (so relative
// apiSpec paths resolve against it) and loads it through Load.
func loadConfig(t *testing.T, dir, yamlContent string) (*GatewayConfig, error) {
	t.Helper()
	path := filepath.Join(dir, "config.yml")
	if err := os.WriteFile(path, []byte(yamlContent), 0o644); err != nil {
		t.Fatalf("failed to write test config: %v", err)
	}
	return Load(path)
}

func TestLoadAcceptsScalarAPISpec(t *testing.T) {
	dir := t.TempDir()
	cfg, err := loadConfig(t, dir, configHeader+`apiSpec: "./api-spec.yaml"`)
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}

	if len(cfg.APISpec) != 1 || cfg.APISpec[0] != "./api-spec.yaml" {
		t.Errorf("APISpec = %+v, want a single entry %q", cfg.APISpec, "./api-spec.yaml")
	}

	want := []string{filepath.Join(dir, "api-spec.yaml")}
	if got := cfg.ResolveSpecPaths(); len(got) != 1 || got[0] != want[0] {
		t.Errorf("ResolveSpecPaths() = %v, want %v", got, want)
	}
}

func TestLoadAcceptsListAPISpec(t *testing.T) {
	dir := t.TempDir()
	cfg, err := loadConfig(t, dir, configHeader+`apiSpec:
  - "./base.yaml"
  - "./overrides.yaml"
`)
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}

	if len(cfg.APISpec) != 2 || cfg.APISpec[0] != "./base.yaml" || cfg.APISpec[1] != "./overrides.yaml" {
		t.Errorf("APISpec = %+v, want [./base.yaml ./overrides.yaml]", cfg.APISpec)
	}

	want := []string{filepath.Join(dir, "base.yaml"), filepath.Join(dir, "overrides.yaml")}
	got := cfg.ResolveSpecPaths()
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("ResolveSpecPaths() = %v, want %v", got, want)
	}
}

func TestLoadRejectsMissingAPISpec(t *testing.T) {
	dir := t.TempDir()
	if _, err := loadConfig(t, dir, configHeader); err == nil {
		t.Fatal("expected an error when apiSpec is not set")
	}
}

func TestLoadRejectsEmptyAPISpecList(t *testing.T) {
	dir := t.TempDir()
	if _, err := loadConfig(t, dir, configHeader+"apiSpec: []"); err == nil {
		t.Fatal("expected an error when apiSpec is an empty list")
	}
}

func TestResolveSpecPathsKeepsAbsolutePaths(t *testing.T) {
	dir := t.TempDir()
	absSpec := filepath.Join(dir, "somewhere-else", "api-spec.yaml")
	cfg, err := loadConfig(t, dir, configHeader+"apiSpec: \""+absSpec+"\"")
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}

	got := cfg.ResolveSpecPaths()
	if len(got) != 1 || got[0] != absSpec {
		t.Errorf("ResolveSpecPaths() = %v, want an absolute apiSpec entry to pass through unchanged (%q)", got, absSpec)
	}
}
