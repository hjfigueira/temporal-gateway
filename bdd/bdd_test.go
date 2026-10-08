// Package bdd is the godog (Cucumber for Go) acceptance suite that runs
// directly against the Gherkin specs in .specs/features/ - see
// .specs/features/README.md (BDD AUTOMATION STATUS) for exactly which
// files this suite executes and why the rest aren't wired up yet.
//
// It only ever calls the gateway's exported API (gateway.NewHandler,
// spec.Load, spec.Routes, ...), the same surface a real embedder would
// use, and drives it over real net/http/httptest requests - a black-box
// acceptance test, not a white-box unit test. It never reaches into an
// internal package's unexported identifiers.
package bdd

import (
	"context"
	"log/slog"
	"testing"

	"github.com/cucumber/godog"
)

// featurePaths lists the exact .specs/features/*.feature files this suite
// executes. Paths are relative to this package's directory (where `go
// test` runs from), reaching up into .specs/ at the repo root.
//
// Only a subset of .specs/features is listed here - the rest describe
// behavior (Temporal dispatch semantics, OpenTelemetry, process signals,
// Docker/CI) that needs a fake/real Temporal SDK client or a live process
// to exercise, which this suite deliberately doesn't attempt yet. See
// .specs/features/README.md for the full list and how to extend this.
var featurePaths = []string{
	"../.specs/features/spec-driven-routing.feature",
	"../.specs/features/workflow-id-templating.feature",
	"../.specs/features/request-validation.feature",
	"../.specs/features/multi-trigger-dispatch.feature",
}

// testLogger is a no-op structured logger, same as every existing
// internal/gateway test (e.g. internal/gateway/dispatch_handler_test.go) -
// a *slog.Logger is required by gateway.NewHandler, but its output isn't
// part of any scenario's assertions.
var testLogger = slog.New(slog.NewTextHandler(discard{}, nil))

// discard is an io.Writer that throws away everything written to it,
// avoiding an io import just for io.Discard's type in testLogger's literal.
type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }

// TestFeatures is the single go test entrypoint for the whole BDD suite:
// `go test ./bdd/...` (or `go test ./...`) runs every scenario in
// featurePaths as its own subtest (TestingT: t), so `go test -run
// TestFeatures/<scenario>` and normal test output/caching work as usual.
func TestFeatures(t *testing.T) {
	suite := godog.TestSuite{
		ScenarioInitializer: InitializeScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    featurePaths,
			Strict:   true, // an undefined/pending/ambiguous step fails the run
			TestingT: t,
		},
	}

	if suite.Run() != 0 {
		t.Fatal("godog: one or more scenarios failed (see output above)")
	}
}

// InitializeScenario registers every step definition and the per-scenario
// reset hook. Step definitions are split across steps_*_test.go by feature
// file, each registering into the same *world so steps shared verbatim
// across feature files (e.g. "the spec is loaded") are defined exactly
// once and reused.
func InitializeScenario(sc *godog.ScenarioContext) {
	w := &world{}

	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		w.reset()
		return ctx, nil
	})

	registerSpecLoadingSteps(sc, w)
	registerRoutingSteps(sc, w)
	registerTemplatingSteps(sc, w)
	registerValidationSteps(sc, w)
	registerDispatchSteps(sc, w)
}
