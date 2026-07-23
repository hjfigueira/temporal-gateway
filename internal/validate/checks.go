package validate

import (
	"fmt"
	"regexp"
)

// typeMatches reports whether data is a JSON value of the schema type named
// want ("string", "number", "integer", "boolean", "object", "array", or
// "null"). An unrecognized want accepts anything, on the assumption that a
// typo in the spec's "type" keyword shouldn't reject every request against
// that route.
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
		return true
	}
}

// checkObject applies schema's "required" and "properties"/
// "additionalProperties" keywords to a JSON object, recursing into walk for
// each declared property present in data.
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

// checkArray applies schema's "items" keyword to every element of a JSON
// array, recursing into walk for each one.
func checkArray(schema map[string]any, data []any, path string, out *[]violation) {
	itemSchema, ok := schema["items"].(map[string]any)
	if !ok {
		return
	}
	for i, item := range data {
		walk(itemSchema, item, fmt.Sprintf("%s[%d]", path, i), out)
	}
}

// checkString applies schema's "minLength", "maxLength", and "pattern"
// keywords to a JSON string value.
func checkString(schema map[string]any, value string, path string, out *[]violation) {
	label := displayField(path)
	if minLen, ok := toInt(schema["minLength"]); ok && len(value) < minLen {
		*out = append(*out, violation{path, msgMinLength(label, minLen)})
	}
	if maxLen, ok := toInt(schema["maxLength"]); ok && len(value) > maxLen {
		*out = append(*out, violation{path, msgMaxLength(label, maxLen)})
	}
	if patternRaw, ok := schema["pattern"].(string); ok {
		re, err := regexp.Compile(patternRaw)
		if err == nil && !re.MatchString(value) {
			*out = append(*out, violation{path, msgFormat(label)})
		}
	}
}

// checkNumber applies schema's "minimum" and "maximum" keywords to a JSON
// number value.
func checkNumber(schema map[string]any, value float64, path string, out *[]violation) {
	label := displayField(path)
	if minVal, ok := toFloat(schema["minimum"]); ok && value < minVal {
		*out = append(*out, violation{path, msgMin(label, minVal)})
	}
	if maxVal, ok := toFloat(schema["maximum"]); ok && value > maxVal {
		*out = append(*out, violation{path, msgMax(label, maxVal)})
	}
}
