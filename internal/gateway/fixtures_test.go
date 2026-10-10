package gateway

import (
	"io"
	"log/slog"
	"strings"
	"testing"

	"temporal-gateway/internal/spec"
)

// TestFixtureSpecsLoadAndRegister keeps tests/*.yaml loadable: each must
// pass spec validation and register every route without ServeMux
// rejecting a pattern.
func TestFixtureSpecsLoadAndRegister(t *testing.T) {
	// api-spec-full.yaml deliberately references ITEMS_TASK_QUEUE with no
	// fallback, so loading it without the variable must fail.
	if _, err := spec.Load("../../tests/api-spec-full.yaml"); err == nil || !strings.Contains(err.Error(), "ITEMS_TASK_QUEUE") {
		t.Fatalf("load without ITEMS_TASK_QUEUE: err = %v, want it named", err)
	}
	t.Setenv("ITEMS_TASK_QUEUE", "items-task-queue")

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	for _, path := range []string{"../../tests/api-spec-full.yaml", "../../tests/api-spec-full-2.yaml"} {
		apiSpec, err := spec.Load(path)
		if err != nil {
			t.Errorf("%s: %v", path, err)
			continue
		}
		if len(apiSpec.Routes()) == 0 {
			t.Errorf("%s: no routes", path)
		}
		NewHandler(apiSpec, &trackingDispatcher{}, Options{}, logger)
	}
}
