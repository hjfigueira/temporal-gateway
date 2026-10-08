package bdd

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/cucumber/godog"

	"temporal-gateway/internal/spec"
)

// registerValidationSteps wires up
// .specs/features/request-validation.feature. Every scenario goes through
// the real end-to-end path (gateway.NewHandler -> dispatchHandler ->
// validateBody -> validate.Schema), driven over HTTP with a
// recordingDispatcher, so "does the request even reach Temporal dispatch"
// is answerable exactly as it would be in production.
func registerValidationSteps(sc *godog.ScenarioContext, w *world) {
	sc.Given(`^an operation declares a requestBody with an application/json schema$`, func() error { return nil })

	sc.Given(`^the schema requires "([^"]+)" and "([^"]+)", both strings$`, func(f1, f2 string) error {
		setRequiredStringsSchema(w, f1, f2)
		return nil
	})
	sc.Given(`^the schema requires "([^"]+)" and "([^"]+)"$`, func(f1, f2 string) error {
		setRequiredStringsSchema(w, f1, f2)
		return nil
	})
	sc.Given(`^the request body has both fields as strings$`, func() error {
		w.validateBody = map[string]any{
			w.requiredFieldNames[0]: "v1",
			w.requiredFieldNames[1]: "v2",
		}
		return nil
	})
	sc.Given(`^the request body only has "([^"]+)"$`, func(field string) error {
		w.validateBody = map[string]any{field: "v1"}
		return nil
	})
	sc.Given(`^the request body has neither field, and an extra field schema forbids$`, func() error {
		w.schema["additionalProperties"] = false
		w.validateBody = map[string]any{"extra": "nope"}
		return nil
	})

	sc.Given(`^a field is declared type "([^"]+)" with a minLength$`, func(typ string) error {
		w.schema = map[string]any{
			"type": "object",
			"properties": map[string]any{
				"code":  map[string]any{"type": typ, "minLength": 5},
				"other": map[string]any{"type": typ, "minLength": 10},
			},
		}
		return nil
	})
	sc.Given(`^the request sends that field as a number instead$`, func() error {
		// "other" is this scenario's sibling field: given a value that
		// violates its own minLength, to prove it's still checked
		// independently of "code"'s type mismatch.
		w.validateBody = map[string]any{"code": 123, "other": "short"}
		return nil
	})

	sc.Given(`^requestBody\.required is true$`, func() error {
		w.requestBodyRequired = true
		return nil
	})
	sc.Given(`^requestBody\.required is false \(or requestBody is absent\)$`, func() error {
		w.requestBodyRequired = false
		return nil
	})
	sc.Given(`^the request has no body$`, func() error {
		w.noBody = true
		return nil
	})
	sc.Given(`^the request body is not valid JSON$`, func() error {
		w.rawBodyOverride = "{"
		return nil
	})

	sc.Given(`^the schema sets additionalProperties: false$`, func() error {
		w.schema = map[string]any{"type": "object", "additionalProperties": false}
		return nil
	})
	sc.Given(`^declares only "([^"]+)" under properties$`, func(field string) error {
		props, _ := w.schema["properties"].(map[string]any)
		if props == nil {
			props = map[string]any{}
		}
		props[field] = map[string]any{"type": "string"}
		w.schema["properties"] = props
		return nil
	})
	sc.Given(`^the request body includes an extra field "([^"]+)"$`, func(field string) error {
		w.validateBody[field] = "x"
		return nil
	})

	sc.Given(`^the schema declares "([^"]+)" as an array of strings$`, func(field string) error {
		w.schema = map[string]any{
			"type": "object",
			"properties": map[string]any{
				field: map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			},
		}
		return nil
	})
	sc.Given(`^the request sends an array with one non-string element at index 1$`, func() error {
		w.validateBody = map[string]any{"items": []any{"a", 123, "c"}}
		return nil
	})

	sc.Given(`^the schema uses "oneOf" or "\$ref" or "format"$`, func() error {
		w.schema = map[string]any{
			"type":  "object",
			"oneOf": []any{map[string]any{}},
			"properties": map[string]any{
				"x": map[string]any{"$ref": "#/components/schemas/Foo", "format": "email"},
			},
		}
		w.validateBody = map[string]any{"x": "anything"}
		return nil
	})

	sc.When(`^the body is validated$`, func() error { return performValidation(w) })
	sc.When(`^the request is decoded$`, func() error { return performValidation(w) })

	sc.Then(`^validation fails with status "([^"]+)"$`, func(want string) error { return checkDecodedStatus(w, want) })
	// "validation reports status VALID" is checked differently than a
	// failure status: on success, dispatchHandler doesn't serialize
	// validate.Result at all - it proceeds to dispatch, and the HTTP
	// response becomes *that* result (e.g. a WorkflowStarted with status
	// STARTED), so a literal "VALID" string is never actually on the
	// wire. "Passed validation" is observable instead as "didn't get the
	// 422 validation-failure shape, and dispatch was reached".
	sc.Then(`^validation reports status "([^"]+)"$`, func(want string) error {
		if want != "VALID" {
			return checkDecodedStatus(w, want)
		}
		return expectValidationPassed(w)
	})

	sc.Then(`^the response's fields map includes "([^"]+)"$`, func(field string) error {
		fields, _ := w.decoded["fields"].(map[string]any)
		if _, ok := fields[field]; !ok {
			return fmt.Errorf("fields map %v does not include %q", fields, field)
		}
		w.lastFieldChecked = field
		return nil
	})
	sc.Then(`^the message is "([^"]+)"$`, func(want string) error {
		if w.lastFieldChecked != "" {
			fields, _ := w.decoded["fields"].(map[string]any)
			msgs, _ := fields[w.lastFieldChecked].([]any)
			for _, m := range msgs {
				if s, ok := m.(string); ok && s == want {
					return nil
				}
			}
			return fmt.Errorf("fields[%q] = %v, want it to include %q", w.lastFieldChecked, msgs, want)
		}
		got, _ := w.decoded["message"].(string)
		if got != want {
			return fmt.Errorf("message = %q, want %q", got, want)
		}
		return nil
	})
	sc.Then(`^the response names all three problems in one 422 response$`, func() error {
		if w.rec.Code != http.StatusUnprocessableEntity {
			return fmt.Errorf("HTTP status = %d, want %d", w.rec.Code, http.StatusUnprocessableEntity)
		}
		fields, _ := w.decoded["fields"].(map[string]any)
		if len(fields) != 3 {
			return fmt.Errorf("fields map has %d entries, want 3: %v", len(fields), fields)
		}
		return nil
	})
	sc.Then(`^only the type violation is reported for that field$`, func() error {
		fields, _ := w.decoded["fields"].(map[string]any)
		msgs, _ := fields["code"].([]any)
		if len(msgs) != 1 {
			return fmt.Errorf(`fields["code"] = %v, want exactly 1 violation`, msgs)
		}
		if s, _ := msgs[0].(string); !strings.Contains(s, "string") {
			return fmt.Errorf(`fields["code"][0] = %q, want it to mention the type violation`, s)
		}
		return nil
	})
	sc.Then(`^minLength is not also evaluated against the wrong-typed value$`, func() error {
		fields, _ := w.decoded["fields"].(map[string]any)
		for _, m := range mustStrings(fields["code"]) {
			if strings.Contains(m, "characters") {
				return fmt.Errorf(`fields["code"] unexpectedly includes a minLength message: %v`, fields["code"])
			}
		}
		return nil
	})
	sc.Then(`^sibling fields are still fully validated independently$`, func() error {
		fields, _ := w.decoded["fields"].(map[string]any)
		if _, ok := fields["other"]; !ok {
			return fmt.Errorf("fields map %v is missing the sibling field's own violation", fields)
		}
		return nil
	})
	sc.Then(`^the request proceeds to Temporal dispatch$`, func() error {
		if !w.dispatchCalled {
			return fmt.Errorf("expected the dispatcher to have been called, it wasn't")
		}
		return nil
	})
	sc.Then(`^the response is (\d+) with status "([^"]+)"$`, func(code int, status string) error {
		if w.rec.Code != code {
			return fmt.Errorf("HTTP status = %d, want %d", w.rec.Code, code)
		}
		return checkDecodedStatus(w, status)
	})
	sc.Then(`^the message names the JSON decoding error$`, func() error {
		msg, _ := w.decoded["message"].(string)
		if !strings.Contains(msg, "invalid JSON body") {
			return fmt.Errorf("message = %q, want it to mention the JSON decoding error", msg)
		}
		return nil
	})
	sc.Then(`^validation fails naming "([^"]+)" as prohibited$`, func(field string) error {
		fields, _ := w.decoded["fields"].(map[string]any)
		for _, m := range mustStrings(fields[field]) {
			if strings.Contains(m, "prohibited") {
				return nil
			}
		}
		return fmt.Errorf("fields[%q] = %v, want a \"prohibited\" message", field, fields[field])
	})
	sc.Then(`^the violation's field path is "([^"]+)"$`, func(path string) error {
		fields, _ := w.decoded["fields"].(map[string]any)
		if _, ok := fields[path]; !ok {
			return fmt.Errorf("fields map %v does not include %q", fields, path)
		}
		return nil
	})
	sc.Then(`^those keywords have no effect on the validation outcome$`, func() error {
		return expectValidationPassed(w)
	})
	sc.Then(`^no error reports them as unsupported$`, func() error {
		if fields, ok := w.decoded["fields"]; ok && fields != nil {
			return fmt.Errorf("expected no fields map, got %v", fields)
		}
		return nil
	})
}

func setRequiredStringsSchema(w *world, f1, f2 string) {
	w.schema = map[string]any{
		"type":     "object",
		"required": []any{f1, f2},
		"properties": map[string]any{
			f1: map[string]any{"type": "string"},
			f2: map[string]any{"type": "string"},
		},
	}
	w.requiredFieldNames = []string{f1, f2}
}

func checkDecodedStatus(w *world, want string) error {
	got, _ := w.decoded["status"].(string)
	if got != want {
		return fmt.Errorf("status = %q, want %q (body: %v)", got, want, w.decoded)
	}
	return nil
}

// expectValidationPassed checks that the request was not rejected by
// validation: not a 422, and dispatch was actually reached. See the doc
// comment on the "validation reports status VALID" registration for why
// this isn't simply checkDecodedStatus(w, "VALID").
func expectValidationPassed(w *world) error {
	if w.rec.Code == http.StatusUnprocessableEntity {
		return fmt.Errorf("HTTP status = 422, want the request to have passed validation (body: %v)", w.decoded)
	}
	if !w.dispatchCalled {
		return fmt.Errorf("expected validation to pass and dispatch to be reached, but the dispatcher was never called")
	}
	return nil
}

// mustStrings coerces a decoded JSON "fields[x]" value ([]any of strings,
// or absent/nil) into a []string, so callers can range over it without a
// type-assertion dance at every call site.
func mustStrings(v any) []string {
	list, _ := v.([]any)
	out := make([]string, 0, len(list))
	for _, item := range list {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// performValidation builds a single-route spec from w.schema/
// requestBodyRequired, registers it with a recordingDispatcher, fires one
// request built from w.validateBody/noBody/rawBodyOverride, and stashes
// the HTTP status, decoded response, and whether dispatch was reached.
func performValidation(w *world) error {
	op := &spec.Operation{
		OperationID: "validateTest",
		RequestBody: &spec.RequestBody{
			Required: w.requestBodyRequired,
			Content:  map[string]spec.MediaType{"application/json": {Schema: w.schema}},
		},
		Temporal: spec.TemporalSpec{Triggers: []spec.TemporalBinding{{
			Action:       spec.ActionStartWorkflow,
			Namespace:    "default",
			WorkflowType: "TestWorkflow",
			TaskQueue:    "test-task-queue",
			WorkflowID:   "validate-test-1",
		}}},
	}
	apiSpec := &spec.Spec{Paths: map[string]spec.PathItem{"/validate-test": {Post: op}}}
	rec := &recordingDispatcher{}
	handler := buildHandler(apiSpec, rec)

	switch {
	case w.rawBodyOverride != "":
		w.rec, w.decoded = fireRequestRaw(handler, http.MethodPost, "/validate-test", w.rawBodyOverride)
	case w.noBody:
		w.rec, w.decoded = fireRequest(handler, http.MethodPost, "/validate-test", nil, nil)
	default:
		w.rec, w.decoded = fireRequest(handler, http.MethodPost, "/validate-test", nil, w.validateBody)
	}
	w.dispatchCalled = rec.called
	return nil
}
