package bdd

import (
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/cucumber/godog"
	"github.com/google/uuid"

	"temporal-gateway/internal/spec"
)

// registerTemplatingSteps wires up
// .specs/features/workflow-id-templating.feature. It proves ADR-004's
// {origin.field}/{uuidv7} rendering by driving a real request through
// gateway.NewHandler with echoDispatcher and reading back the rendered
// workflow ID the handler resolved - the same renderTemplate/fieldResolver
// code path production uses, exercised through its public HTTP contract
// rather than by reaching into internal/gateway's unexported functions
// directly.
func registerTemplatingSteps(sc *godog.ScenarioContext, w *world) {
	sc.Given(`^a trigger's workflowId is a template string containing placeholders$`, func() error { return nil })

	sc.Given(`^workflowId is "([^"]*)"$`, func(tmpl string) error {
		w.tmpl = tmpl
		return nil
	})
	sc.Given(`^workflowId contains "([^"]+)"$`, func(tmpl string) error {
		w.tmpl = tmpl
		return nil
	})

	sc.Given(`^the request has (path|body|query|header) "([^"]+)" = "([^"]+)"$`, func(origin, field, value string) error {
		switch origin {
		case "path":
			w.pathParams[field] = value
		case "body":
			w.bodyFields[field] = value
		case "query":
			w.queryParams[field] = []string{value}
		case "header":
			w.headers[field] = []string{value}
		}
		return nil
	})
	sc.Given(`^path param "([^"]+)" is "([^"]+)" and body field "([^"]+)" is "([^"]+)"$`, func(pField, pVal, bField, bVal string) error {
		w.pathParams[pField] = pVal
		w.bodyFields[bField] = bVal
		return nil
	})
	sc.Given(`^the request body has no "([^"]+)"$`, func(field string) error {
		if _, ok := w.bodyFields[field]; ok {
			return fmt.Errorf("expected body field %q to be absent from this scenario's setup", field)
		}
		return nil
	})
	sc.Given(`^body field "([^"]+)" is a JSON object, not a string$`, func(field string) error {
		w.bodyFields[field] = map[string]any{"a": 1}
		return nil
	})

	sc.When(`^the template is rendered$`, func() error {
		status, decoded := renderTemplateViaHTTP(w)
		w.lastStatus = status
		id, _ := decoded["workflowId"].(string)
		w.renderedIDs = append(w.renderedIDs, id)
		return nil
	})
	sc.When(`^the template is rendered twice for two separate requests$`, func() error {
		for i := 0; i < 2; i++ {
			status, decoded := renderTemplateViaHTTP(w)
			w.lastStatus = status
			id, _ := decoded["workflowId"].(string)
			w.renderedIDs = append(w.renderedIDs, id)
		}
		return nil
	})

	sc.Then(`^the resulting workflow ID is (?:literally )?"([^"]*)"$`, func(want string) error {
		if len(w.renderedIDs) == 0 {
			return fmt.Errorf("no rendering happened yet")
		}
		got := w.renderedIDs[len(w.renderedIDs)-1]
		if got != want {
			return fmt.Errorf("rendered workflow ID = %q, want %q", got, want)
		}
		return nil
	})
	sc.Then(`^each rendering produces a different, valid UUIDv7$`, func() error {
		if len(w.renderedIDs) != 2 {
			return fmt.Errorf("expected 2 renderings, got %d: %v", len(w.renderedIDs), w.renderedIDs)
		}
		if w.renderedIDs[0] == w.renderedIDs[1] {
			return fmt.Errorf("expected two distinct uuids, got %q twice", w.renderedIDs[0])
		}
		for _, id := range w.renderedIDs {
			trimmed := strings.TrimPrefix(id, "job-")
			parsed, err := uuid.Parse(trimmed)
			if err != nil {
				return fmt.Errorf("rendered id %q is not a valid uuid: %w", id, err)
			}
			if parsed.Version() != 7 {
				return fmt.Errorf("rendered id %q has uuid version %d, want 7", id, parsed.Version())
			}
		}
		return nil
	})
	sc.Then(`^neither rendering depends on any request field being present$`, func() error {
		if len(w.pathParams) != 0 || len(w.bodyFields) != 0 || len(w.queryParams) != 0 || len(w.headers) != 0 {
			return fmt.Errorf("expected this scenario to set up no request fields at all")
		}
		return nil
	})
	sc.Then(`^the request is not rejected because of it$`, func() error { return expectHTTPNotRejected(w) })
	sc.Then(`^rendering does not error or reject the request$`, func() error { return expectHTTPNotRejected(w) })
	sc.Then(`^the object renders as its Go-syntax representation$`, func() error {
		last := w.renderedIDs[len(w.renderedIDs)-1]
		if !strings.Contains(last, "map[") {
			return fmt.Errorf("rendered id %q doesn't look like fmt.Sprint of a map (expected to contain \"map[\")", last)
		}
		return nil
	})
	sc.Then(`^"cookie" is not a recognized origin \(only path/body/query/header/uuidv7 are\)$`, func() error {
		// Nothing to execute: the only recognized origins are checked by
		// construction in fieldResolver (see internal/gateway/template.go).
		// The real proof is the next step, that the placeholder survives
		// unresolved - exactly like any other origin it can't satisfy.
		return nil
	})
	sc.Then(`^the placeholder is left untouched, same as an unresolved field$`, func() error {
		last := w.renderedIDs[len(w.renderedIDs)-1]
		if last != w.tmpl {
			return fmt.Errorf("rendered id = %q, want it unchanged from the original template %q", last, w.tmpl)
		}
		return nil
	})
}

func expectHTTPNotRejected(w *world) error {
	if w.lastStatus >= http.StatusBadRequest {
		return fmt.Errorf("HTTP status = %d, want a non-error status", w.lastStatus)
	}
	return nil
}

// renderTemplateViaHTTP builds a one-route spec whose single startWorkflow
// trigger's workflowId is w.tmpl, registers it with echoDispatcher, and
// fires a request built from w.pathParams/bodyFields/queryParams/headers -
// returning the HTTP status and the decoded JSON response (whose
// "workflowId" field is exactly what renderTemplate produced).
func renderTemplateViaHTTP(w *world) (int, map[string]any) {
	pattern, reqPath := "/items", "/items"
	keys := make([]string, 0, len(w.pathParams))
	for k := range w.pathParams {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		pattern += "/{" + k + "}"
		reqPath += "/" + url.PathEscape(w.pathParams[k])
	}

	if len(w.queryParams) > 0 {
		q := url.Values{}
		for k, vs := range w.queryParams {
			for _, v := range vs {
				q.Add(k, v)
			}
		}
		reqPath += "?" + q.Encode()
	}

	op := &spec.Operation{
		OperationID: "testOp",
		Temporal: spec.TemporalSpec{Triggers: []spec.TemporalBinding{{
			Action:       spec.ActionStartWorkflow,
			Namespace:    "default",
			WorkflowType: "TestWorkflow",
			TaskQueue:    "test-task-queue",
			WorkflowID:   w.tmpl,
		}}},
	}
	apiSpec := &spec.Spec{Paths: map[string]spec.PathItem{pattern: {Post: op}}}
	handler := buildHandler(apiSpec, echoDispatcher{})

	var body any
	if len(w.bodyFields) > 0 {
		body = w.bodyFields
	}

	rec, decoded := fireRequest(handler, http.MethodPost, reqPath, w.headers, body)
	return rec.Code, decoded
}
