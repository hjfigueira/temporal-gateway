// Package validate checks a decoded JSON request body against the
// OpenAPI-style JSON Schema fragment declared under an operation's
// requestBody.content["application/json"].schema in the API spec. It
// implements the subset of JSON Schema the gateway's specs actually use:
// type, required, properties, additionalProperties, items, enum,
// minLength/maxLength, minimum/maximum, and pattern.
package validate

import (
	"encoding/json"
	"fmt"

	"temporal-gateway/internal/response"
)

// rootField is the Fields key used for violations that apply to the payload
// as a whole rather than to a specific named field, e.g. the body isn't a
// JSON object at all.
const rootField = "_body"

// Result is the outcome of validating a payload against a schema: the
// shared response.Envelope (status/message) plus, on failure, every rule
// broken per field.
type Result struct {
	response.Envelope
	Fields map[string][]string `json:"fields,omitempty"`
}

// violation is one broken rule found while walking the schema: field is the
// dotted/indexed path to the offending value (e.g. "items[1]", or "" for
// the payload as a whole), and rule is its human-readable message.
type violation struct {
	field string
	rule  string
}

// Schema validates data against schema and collects every violation found,
// rather than stopping at the first, so a caller can report all of them at
// once. A nil or empty schema always passes.
func Schema(schema map[string]any, data any) Result {
	var violations []violation
	walk(schema, data, "", &violations)

	if len(violations) == 0 {
		return Result{Envelope: response.Envelope{Status: response.StatusValid}}
	}

	fields := make(map[string][]string, len(violations))
	for _, v := range violations {
		field := v.field
		if field == "" {
			field = rootField
		}
		fields[field] = append(fields[field], v.rule)
	}

	return Result{
		Envelope: response.Envelope{
			Status:  response.StatusValidationFailed,
			Message: fmt.Sprintf("validation failed: %d issue(s) found", len(violations)),
		},
		Fields: fields,
	}
}

// walk validates data against schema at path, dispatching to the
// type-specific checkers in checks.go for whichever JSON type data actually
// is. A type mismatch short-circuits the rest of schema's keywords for this
// node: checking minLength/properties/items/etc against a value of the
// wrong type would just add noise on top of the type error.
func walk(schema map[string]any, data any, path string, out *[]violation) {
	if len(schema) == 0 {
		return
	}

	if want, ok := schema["type"].(string); ok && !typeMatches(want, data) {
		*out = append(*out, violation{path, msgType(displayField(path), want)})
		return
	}

	if allowed, ok := schema["enum"].([]any); ok && !containsValue(allowed, data) {
		*out = append(*out, violation{path, msgEnum(displayField(path))})
	}

	switch v := data.(type) {
	case string:
		checkString(schema, v, path, out)
	case float64:
		checkNumber(schema, v, path, out)
	case json.Number:
		if f, ok := toFloat(v); ok {
			checkNumber(schema, f, path, out)
		}
	case map[string]any:
		checkObject(schema, v, path, out)
	case []any:
		checkArray(schema, v, path, out)
	}
}
