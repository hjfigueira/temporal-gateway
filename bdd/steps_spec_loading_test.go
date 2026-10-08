package bdd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/cucumber/godog"

	"temporal-gateway/internal/spec"
)

// registerSpecLoadingSteps registers the handful of steps about loading a
// raw YAML api-spec through spec.Load that are shared verbatim (identical
// step text) across more than one feature file:
//   - "the spec is loaded"                      (When)
//   - the two differently-worded "spec loading fails before ... starts"
//     assertions (Then), both in spec-driven-routing.feature and
//     multi-trigger-dispatch.feature.
func registerSpecLoadingSteps(sc *godog.ScenarioContext, w *world) {
	sc.When(`^the spec is loaded$`, func() error {
		dir, path, err := writeTempSpec(w.rawSpec)
		if err != nil {
			return err
		}
		defer os.RemoveAll(dir)
		w.loadedS, w.specErr = spec.Load(path)
		return nil
	})

	sc.Then(`^spec loading fails before the HTTP server starts$`, func() error {
		return expectSpecLoadFailed(w)
	})
	sc.Then(`^spec loading fails before the server starts$`, func() error {
		return expectSpecLoadFailed(w)
	})
}

func expectSpecLoadFailed(w *world) error {
	if w.specErr == nil {
		return fmt.Errorf("spec.Load succeeded, want it to fail")
	}
	return nil
}

// writeTempSpec writes yamlContent to a fresh temp file and returns its
// directory (for the caller to clean up) and path, mirroring
// internal/spec/spec_test.go's loadSpec helper so spec.Load is exercised
// exactly as it is in production (reading a real file, expanding env
// references, parsing YAML) rather than via some bypass.
func writeTempSpec(yamlContent string) (dir, path string, err error) {
	dir, err = os.MkdirTemp("", "bdd-spec-*")
	if err != nil {
		return "", "", err
	}
	path = filepath.Join(dir, "api-spec.yaml")
	if err := os.WriteFile(path, []byte(yamlContent), 0o644); err != nil {
		return "", "", err
	}
	return dir, path, nil
}
