package temporal

import (
	"fmt"
	"time"

	sdktemporal "go.temporal.io/sdk/temporal"

	"temporal-gateway/internal/numeric"
	"temporal-gateway/internal/spec"
)

// buildTypedSearchAttributes converts x-temporal's YAML-friendly
// {name, type, value} search attribute list into the SDK's typed
// search attribute collection. client.StartWorkflowOptions.SearchAttributes
// (a plain map[string]interface{}) is deprecated in favor of
// TypedSearchAttributes, which requires each attribute to be built through
// an explicitly typed key (SearchAttributeKeyString, SearchAttributeKeyBool,
// ...) - hence needing to know each attribute's declared Type here.
// spec.validate already checked every attribute's Value matches its Type,
// so a conversion error here indicates a bug in that validation rather than
// bad input.
func buildTypedSearchAttributes(attrs []spec.SearchAttribute) (sdktemporal.SearchAttributes, error) {
	updates := make([]sdktemporal.SearchAttributeUpdate, 0, len(attrs))
	for _, sa := range attrs {
		update, err := searchAttributeUpdate(sa)
		if err != nil {
			return sdktemporal.SearchAttributes{}, fmt.Errorf("%q: %w", sa.Name, err)
		}
		updates = append(updates, update)
	}
	return sdktemporal.NewSearchAttributes(updates...), nil
}

// searchAttributeUpdate builds the SDK update for a single search
// attribute, dispatching on its declared Type.
func searchAttributeUpdate(sa spec.SearchAttribute) (sdktemporal.SearchAttributeUpdate, error) {
	switch sa.Type {
	case "string":
		v, ok := sa.Value.(string)
		if !ok {
			return nil, fmt.Errorf("value must be a string")
		}
		return sdktemporal.NewSearchAttributeKeyString(sa.Name).ValueSet(v), nil
	case "keyword":
		v, ok := sa.Value.(string)
		if !ok {
			return nil, fmt.Errorf("value must be a string")
		}
		return sdktemporal.NewSearchAttributeKeyKeyword(sa.Name).ValueSet(v), nil
	case "bool":
		v, ok := sa.Value.(bool)
		if !ok {
			return nil, fmt.Errorf("value must be a boolean")
		}
		return sdktemporal.NewSearchAttributeKeyBool(sa.Name).ValueSet(v), nil
	case "int":
		v, ok := toInt64(sa.Value)
		if !ok {
			return nil, fmt.Errorf("value must be an integer")
		}
		return sdktemporal.NewSearchAttributeKeyInt64(sa.Name).ValueSet(v), nil
	case "float":
		v, ok := numeric.ToFloat64(sa.Value)
		if !ok {
			return nil, fmt.Errorf("value must be a number")
		}
		return sdktemporal.NewSearchAttributeKeyFloat64(sa.Name).ValueSet(v), nil
	case "time":
		s, ok := sa.Value.(string)
		if !ok {
			return nil, fmt.Errorf("value must be an RFC3339 string")
		}
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			return nil, fmt.Errorf("value %q is not a valid RFC3339 timestamp: %w", s, err)
		}
		return sdktemporal.NewSearchAttributeKeyTime(sa.Name).ValueSet(t), nil
	case "keywordList":
		list, ok := sa.Value.([]any)
		if !ok {
			return nil, fmt.Errorf("value must be a list of strings")
		}
		values := make([]string, len(list))
		for i, v := range list {
			s, ok := v.(string)
			if !ok {
				return nil, fmt.Errorf("value must be a list of strings")
			}
			values[i] = s
		}
		return sdktemporal.NewSearchAttributeKeyKeywordList(sa.Name).ValueSet(values), nil
	default:
		return nil, fmt.Errorf("unknown type %q", sa.Type)
	}
}

// toInt64 coerces a YAML-decoded numeric value (gopkg.in/yaml.v3 decodes
// plain integers as int) into an int64.
func toInt64(v any) (int64, bool) {
	switch n := v.(type) {
	case int64:
		return n, true
	case int:
		return int64(n), true
	}
	return 0, false
}
