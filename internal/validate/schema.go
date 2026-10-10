package validate

import (
	"errors"
	"fmt"
	"regexp"
	"sync"
)

// knownTypes are the "type" values typeMatches understands.
var knownTypes = map[string]bool{
	"string": true, "number": true, "integer": true, "boolean": true,
	"object": true, "array": true, "null": true,
}

// patterns caches every compiled "pattern", so a request never recompiles
// one. Specs are fixed at startup, so it only grows during CheckSchema.
var patterns sync.Map // string -> *regexp.Regexp

func compilePattern(pattern string) (*regexp.Regexp, error) {
	if re, ok := patterns.Load(pattern); ok {
		return re.(*regexp.Regexp), nil
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, err
	}
	patterns.Store(pattern, re)
	return re, nil
}

// CheckSchema reports every "type" Schema doesn't understand and every
// "pattern" that doesn't compile, so a typo in the spec fails at load
// instead of silently disabling that check (ADR-008).
func CheckSchema(schema map[string]any) error {
	var errs []error
	checkSchema(schema, "", &errs)
	return errors.Join(errs...)
}

func checkSchema(schema map[string]any, path string, errs *[]error) {
	label := displayField(path)
	if want, ok := schema["type"].(string); ok && !knownTypes[want] {
		*errs = append(*errs, fmt.Errorf("%s: unknown type %q", label, want))
	}
	if pattern, ok := schema["pattern"].(string); ok {
		if _, err := compilePattern(pattern); err != nil {
			*errs = append(*errs, fmt.Errorf("%s: invalid pattern: %w", label, err))
		}
	}
	properties, _ := schema["properties"].(map[string]any)
	for name, prop := range properties {
		if propSchema, ok := prop.(map[string]any); ok {
			checkSchema(propSchema, joinPath(path, name), errs)
		}
	}
	if items, ok := schema["items"].(map[string]any); ok {
		checkSchema(items, path+"[]", errs)
	}
}
