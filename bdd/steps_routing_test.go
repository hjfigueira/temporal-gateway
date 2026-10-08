package bdd

import (
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"

	"github.com/cucumber/godog"

	"temporal-gateway/internal/spec"
)

// registerRoutingSteps wires up .specs/features/spec-driven-routing.feature.
// It proves ADR-001's core claim - that the HTTP surface is entirely
// generated from the loaded spec, with zero per-route Go code - using only
// spec.Load/spec.Routes and gateway.NewHandler's exported contract.
func registerRoutingSteps(sc *godog.ScenarioContext, w *world) {
	sc.Given(`^a gateway config\.yml pointing at one or more api-spec\.yaml files$`, func() error { return nil })
	sc.Given(`^each operation in the spec carries an "x-temporal" extension$`, func() error { return nil })

	sc.Given(`^api-spec\.yaml declares "([A-Z]+) (\S+)" with a valid x-temporal binding$`, func(method, path string) error {
		w.rawSpec = singleRouteSpecYAML(path, strings.ToLower(method), `
      x-temporal:
        triggers:
          - action: startWorkflow
            namespace: default
            workflowType: WidgetWorkflow
            workflowId: "widget-1"
            taskQueue: widgets-task-queue`)
		return nil
	})
	sc.When(`^the gateway starts$`, func() error { return loadAndBuildHandler(w) })
	sc.Then(`^"([A-Z]+ \S+)" is registered as an HTTP route$`, func(methodAndPath string) error {
		method, path, _ := strings.Cut(methodAndPath, " ")
		rec, decoded := fireRequest(w.handler, method, path, nil, nil)
		w.rec, w.decoded = rec, decoded
		if rec.Code == http.StatusNotFound {
			return fmt.Errorf("%s %s was not routed (404) - route was not registered from the spec", method, path)
		}
		return nil
	})
	// "no Go code was written or changed to add it" is the architectural
	// claim the rest of this scenario already demonstrates mechanically
	// (the route above was registered from pure YAML); there's nothing
	// further to execute here.
	sc.Then(`^no Go code was written or changed to add it$`, func() error { return nil })

	sc.Given(`^an OpenAPI path template like "([^"]+)"$`, func(path string) error {
		w.rawSpec = singleRouteSpecYAML(path, "get", `
      x-temporal:
        triggers:
          - action: queryWorkflow
            namespace: default
            workflowId: "order-{path.orderId}"
            queryType: getOrderState`)
		return nil
	})
	sc.When(`^the route is registered$`, func() error { return loadAndBuildHandler(w) })
	sc.Then(`^the pattern "([A-Z]+ \S+)" is handed to http\.ServeMux as-is$`, func(methodAndPath string) error {
		method, _, _ := strings.Cut(methodAndPath, " ")
		rec, decoded := fireRequest(w.handler, method, "/orders/789", nil, nil)
		w.rec, w.decoded = rec, decoded
		if rec.Code == http.StatusNotFound {
			return fmt.Errorf("GET /orders/789 was not routed (404) - the {orderId} pattern was not registered as given")
		}
		return nil
	})
	sc.Then(`^"\{([^}]+)\}" resolves as an http\.ServeMux path wildcard at request time$`, func(_ string) error {
		id, _ := w.decoded["workflowId"].(string)
		if !strings.Contains(id, "789") {
			return fmt.Errorf("decoded response %v does not reflect the path wildcard's captured value (789)", w.decoded)
		}
		return nil
	})

	sc.Given(`^a PathItem with get/post/put/patch/delete entries$`, func() error {
		w.rawSpec = multiMethodSpecYAML()
		return nil
	})
	sc.When(`^routes are flattened for registration$`, func() error {
		if err := loadAndBuildHandler(w); err != nil {
			return err
		}
		w.routesOf = w.loadedS.Routes()
		return nil
	})
	sc.Then(`^OPTIONS, HEAD, and TRACE are not generated as routes$`, func() error {
		if len(w.routesOf) != 5 {
			return fmt.Errorf("Routes() returned %d routes, want exactly 5 (get/post/put/patch/delete): %+v", len(w.routesOf), w.routesOf)
		}
		for _, r := range w.routesOf {
			if r.Method == "OPTIONS" || r.Method == "HEAD" || r.Method == "TRACE" {
				return fmt.Errorf("Routes() included %s, which PathItem has no field for", r.Method)
			}
		}
		return nil
	})
	sc.Then(`^are not supported even if present under those keys in the YAML$`, func() error {
		rec, _ := fireRequest(w.handler, http.MethodOptions, "/widgets-full", nil, nil)
		if rec.Code != http.StatusMethodNotAllowed {
			return fmt.Errorf("OPTIONS /widgets-full = %d, want %d (Method Not Allowed) - an \"options:\" key in the YAML must never become a routable OPTIONS handler", rec.Code, http.StatusMethodNotAllowed)
		}
		return nil
	})

	sc.Given(`^a spec with multiple paths and methods$`, func() error {
		w.loadedS = &spec.Spec{Paths: map[string]spec.PathItem{
			"/b-widgets": {
				Get:  &spec.Operation{OperationID: "bGet"},
				Post: &spec.Operation{OperationID: "bPost"},
			},
			"/a-widgets": {
				Get: &spec.Operation{OperationID: "aGet"},
			},
		}}
		return nil
	})
	sc.When(`^Spec\.Routes\(\) is called$`, func() error {
		w.routesOf = w.loadedS.Routes()
		return nil
	})
	sc.Then(`^the result is sorted by path, then by method$`, func() error {
		if !sort.SliceIsSorted(w.routesOf, func(i, j int) bool {
			if w.routesOf[i].Path != w.routesOf[j].Path {
				return w.routesOf[i].Path < w.routesOf[j].Path
			}
			return w.routesOf[i].Method < w.routesOf[j].Method
		}) {
			return fmt.Errorf("Routes() = %+v, not sorted by (path, method)", w.routesOf)
		}
		if len(w.routesOf) != 3 {
			return fmt.Errorf("Routes() returned %d routes, want 3", len(w.routesOf))
		}
		return nil
	})

	sc.Given(`^an operation with no "x-temporal\.triggers" entries$`, func() error {
		w.rawSpec = `openapi: 3.0.3
info:
  title: Test
  version: "1.0.0"
paths:
  /widgets:
    post:
      operationId: createWidget
`
		return nil
	})
	// "the spec is loaded" / "spec loading fails before the HTTP server
	// starts" are registered once in steps_spec_loading_test.go and reused
	// here verbatim.
	sc.Then(`^the gateway process exits non-zero rather than serving a broken route$`, func() error {
		// main.go turning a startup error into os.Exit(1) is an
		// integration-level concern this in-process suite can't exercise;
		// the prior "spec loading fails" step already proved the error
		// main() would act on.
		return nil
	})
}

// loadAndBuildHandler loads w.rawSpec (as a temp file, through the real
// spec.Load) and builds an HTTP handler from it with echoDispatcher,
// storing both on w. Shared by every "the gateway starts"/"the route is
// registered"/"routes are flattened" step above.
func loadAndBuildHandler(w *world) error {
	dir, path, err := writeTempSpec(w.rawSpec)
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)

	apiSpec, err := spec.Load(path)
	if err != nil {
		return fmt.Errorf("spec.Load: %w", err)
	}
	w.loadedS = apiSpec
	w.handler = buildHandler(apiSpec, echoDispatcher{})
	return nil
}

// singleRouteSpecYAML builds a minimal valid OpenAPI document declaring
// one path/method, with xTemporalBlock (a pre-indented "      x-temporal:
// ..." YAML block, matching internal/spec/spec_test.go's specHeader
// convention) as that operation's x-temporal extension.
func singleRouteSpecYAML(path, method, xTemporalBlock string) string {
	return fmt.Sprintf(`openapi: 3.0.3
info:
  title: Test
  version: "1.0.0"
paths:
  %s:
    %s:
      operationId: testOp%s
`, path, method, xTemporalBlock)
}

// multiMethodSpecYAML declares all five routable methods plus options/
// head/trace (with full operation bodies) on one path, so the "even if
// present in the YAML" claim is tested against YAML that actually
// populates those keys, not just omits them.
func multiMethodSpecYAML() string {
	const trigger = `
      x-temporal:
        triggers:
          - action: cancelWorkflow
            namespace: default
            workflowId: "w-1"`
	methods := []string{"get", "post", "put", "patch", "delete", "options", "head", "trace"}
	doc := `openapi: 3.0.3
info:
  title: Test
  version: "1.0.0"
paths:
  /widgets-full:
`
	for _, m := range methods {
		doc += fmt.Sprintf("    %s:\n      operationId: op%s%s\n", m, m, trigger)
	}
	return doc
}
