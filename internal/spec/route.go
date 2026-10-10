package spec

import (
	"sort"
	"strings"

	"github.com/getkin/kin-openapi/routers"
)

// Route is a flattened (method, path, operation) triple, convenient for
// iterating over the spec when registering HTTP handlers.
type Route struct {
	Method    string
	Path      string
	Operation *Operation
	// OpenAPI is the same operation as kin-openapi sees it, which requests
	// are validated against (ADR-029). Nil for a Spec not built by Load.
	OpenAPI *routers.Route
}

// Routes flattens the spec's paths into a stable, sorted list of routes.
func (s *Spec) Routes() []Route {
	routes := make([]Route, 0, len(s.Paths))
	for path, item := range s.Paths {
		for method, op := range item.operations() {
			routes = append(routes, Route{Method: method, Path: path, Operation: op, OpenAPI: s.openAPIRoute(method, path)})
		}
	}
	sort.Slice(routes, func(i, j int) bool {
		if routes[i].Path != routes[j].Path {
			return routes[i].Path < routes[j].Path
		}
		return routes[i].Method < routes[j].Method
	})
	return routes
}

// openAPIRoute returns kin-openapi's route for method and path, or nil
// when s wasn't built by Load.
func (s *Spec) openAPIRoute(method, path string) *routers.Route {
	if s.doc == nil {
		return nil
	}
	item := s.doc.Paths.Value(path)
	return &routers.Route{Spec: s.doc, Path: path, PathItem: item, Method: method, Operation: item.GetOperation(method)}
}

// operations returns p's declared methods keyed by their HTTP verb, so
// callers can iterate without a per-method nil check.
func (p PathItem) operations() map[string]*Operation {
	ops := map[string]*Operation{}
	if p.Get != nil {
		ops["GET"] = p.Get
	}
	if p.Post != nil {
		ops["POST"] = p.Post
	}
	if p.Put != nil {
		ops["PUT"] = p.Put
	}
	if p.Patch != nil {
		ops["PATCH"] = p.Patch
	}
	if p.Delete != nil {
		ops["DELETE"] = p.Delete
	}
	return ops
}

// PathParamNames returns the {curly-brace} placeholder names declared in a
// path template, e.g. "/orders/{orderId}" -> ["orderId"].
func PathParamNames(path string) []string {
	var names []string
	for {
		start := strings.IndexByte(path, '{')
		if start == -1 {
			break
		}
		end := strings.IndexByte(path[start:], '}')
		if end == -1 {
			break
		}
		names = append(names, path[start+1:start+end])
		path = path[start+end+1:]
	}
	return names
}
