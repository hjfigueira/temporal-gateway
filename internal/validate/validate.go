// Package validate checks a decoded JSON request body against the
// OpenAPI-style JSON Schema fragment declared under an operation's
// requestBody.content["application/json"].schema in the API spec. It
// implements the subset of JSON Schema the gateway's specs actually use:
// type, required, properties, additionalProperties, items, enum,
// minLength/maxLength, minimum/maximum, and pattern.
package validate

import (
	"fmt"
	"regexp"

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

type violation struct {
	field string
	rule  string
}

func walk(schema map[string]any, data any, path string, out *[]violation) {
	if len(schema) == 0 {
		return
	}

	if want, ok := schema["type"].(string); ok && !typeMatches(want, data) {
		*out = append(*out, violation{path, msgType(displayField(path), want)})
		// Further keyword checks (properties, items, minLength, ...) assume
		// the declared type, so they'd just add noise once it's wrong.
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
	case map[string]any:
		checkObject(schema, v, path, out)
	case []any:
		checkArray(schema, v, path, out)
	}
}

func typeMatches(want string, data any) bool {
	switch want {
	case "string":
		_, ok := data.(string)
		return ok
	case "number":
		_, ok := data.(float64)
		return ok
	case "integer":
		f, ok := data.(float64)
		return ok && f == float64(int64(f))
	case "boolean":
		_, ok := data.(bool)
		return ok
	case "object":
		_, ok := data.(map[string]any)
		return ok
	case "array":
		_, ok := data.([]any)
		return ok
	case "null":
		return data == nil
	default:
		// Unknown declared type: accept anything rather than rejecting a
		// request over a spec typo.
		return true
	}
}

func checkObject(schema map[string]any, data map[string]any, path string, out *[]violation) {
	if requiredRaw, ok := schema["required"].([]any); ok {
		for _, r := range requiredRaw {
			name, ok := r.(string)
			if !ok {
				continue
			}
			if _, present := data[name]; !present {
				fieldPath := joinPath(path, name)
				*out = append(*out, violation{fieldPath, msgRequired(fieldPath)})
			}
		}
	}

	properties, _ := schema["properties"].(map[string]any)
	additionalAllowed, additionalDeclared := schema["additionalProperties"].(bool)

	for name, value := range data {
		fieldPath := joinPath(path, name)
		propSchemaRaw, declared := properties[name]
		if !declared {
			if additionalDeclared && !additionalAllowed {
				*out = append(*out, violation{fieldPath, msgProhibited(fieldPath)})
			}
			continue
		}
		propSchema, ok := propSchemaRaw.(map[string]any)
		if !ok {
			continue
		}
		walk(propSchema, value, fieldPath, out)
	}
}

func checkArray(schema map[string]any, data []any, path string, out *[]violation) {
	itemSchema, ok := schema["items"].(map[string]any)
	if !ok {
		return
	}
	for i, item := range data {
		walk(itemSchema, item, fmt.Sprintf("%s[%d]", path, i), out)
	}
}

func checkString(schema map[string]any, value string, path string, out *[]violation) {
	label := displayField(path)
	if min, ok := toInt(schema["minLength"]); ok && len(value) < min {
		*out = append(*out, violation{path, msgMinLength(label, min)})
	}
	if max, ok := toInt(schema["maxLength"]); ok && len(value) > max {
		*out = append(*out, violation{path, msgMaxLength(label, max)})
	}
	if patternRaw, ok := schema["pattern"].(string); ok {
		re, err := regexp.Compile(patternRaw)
		if err == nil && !re.MatchString(value) {
			*out = append(*out, violation{path, msgFormat(label)})
		}
	}
}

func checkNumber(schema map[string]any, value float64, path string, out *[]violation) {
	label := displayField(path)
	if min, ok := toFloat(schema["minimum"]); ok && value < min {
		*out = append(*out, violation{path, msgMin(label, min)})
	}
	if max, ok := toFloat(schema["maximum"]); ok && value > max {
		*out = append(*out, violation{path, msgMax(label, max)})
	}
}

func joinPath(path, name string) string {
	if path == "" {
		return name
	}
	return path + "." + name
}

// displayField returns the name to use inside a message's "The X ..."
// phrase. Field-level violations (required, prohibited) always have a
// non-empty path already; this only matters for a violation on the payload
// as a whole, e.g. the body isn't a JSON object at all.
func displayField(path string) string {
	if path == "" {
		return "payload"
	}
	return path
}

func containsValue(allowed []any, data any) bool {
	for _, a := range allowed {
		if valuesEqual(a, data) {
			return true
		}
	}
	return false
}

func valuesEqual(a, b any) bool {
	if af, ok := toFloat(a); ok {
		bf, ok := toFloat(b)
		return ok && af == bf
	}
	return fmt.Sprint(a) == fmt.Sprint(b)
}

func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	}
	return 0, false
}

func toInt(v any) (int, bool) {
	f, ok := toFloat(v)
	if !ok {
		return 0, false
	}
	return int(f), true
}

// The functions below build violation messages in the style of Laravel's
// default validation messages (see Laravel's lang/en/validation.php),
// substituting the field path for Laravel's ":attribute".

func msgRequired(field string) string {
	return fmt.Sprintf("The %s field is required.", field)
}

func msgType(field, want string) string {
	switch want {
	case "string":
		return fmt.Sprintf("The %s must be a string.", field)
	case "number":
		return fmt.Sprintf("The %s must be a number.", field)
	case "integer":
		return fmt.Sprintf("The %s must be an integer.", field)
	case "boolean":
		return fmt.Sprintf("The %s field must be true or false.", field)
	case "array":
		return fmt.Sprintf("The %s must be an array.", field)
	case "object":
		return fmt.Sprintf("The %s must be an object.", field)
	case "null":
		return fmt.Sprintf("The %s field must be null.", field)
	default:
		return fmt.Sprintf("The %s is invalid.", field)
	}
}

func msgEnum(field string) string {
	return fmt.Sprintf("The selected %s is invalid.", field)
}

func msgMinLength(field string, min int) string {
	return fmt.Sprintf("The %s must be at least %d characters.", field, min)
}

func msgMaxLength(field string, max int) string {
	return fmt.Sprintf("The %s must not be greater than %d characters.", field, max)
}

func msgFormat(field string) string {
	return fmt.Sprintf("The %s format is invalid.", field)
}

func msgMin(field string, min float64) string {
	return fmt.Sprintf("The %s must be at least %v.", field, min)
}

func msgMax(field string, max float64) string {
	return fmt.Sprintf("The %s must not be greater than %v.", field, max)
}

func msgProhibited(field string) string {
	return fmt.Sprintf("The %s field is prohibited.", field)
}
