package validate

import (
	"fmt"

	"temporal-gateway/internal/numeric"
)

// joinPath appends name to a dotted field path, e.g. joinPath("customer",
// "city") -> "customer.city". A blank path (the schema root) yields just
// name.
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

// containsValue reports whether data equals any member of allowed, per
// valuesEqual.
func containsValue(allowed []any, data any) bool {
	for _, a := range allowed {
		if valuesEqual(a, data) {
			return true
		}
	}
	return false
}

// valuesEqual compares two decoded JSON/YAML scalars for equality,
// numerically if both sides are numbers (so e.g. YAML's `int` and JSON's
// `float64` compare equal) and by string representation otherwise.
func valuesEqual(a, b any) bool {
	if af, ok := numeric.ToFloat64(a); ok {
		bf, ok := numeric.ToFloat64(b)
		return ok && af == bf
	}
	return fmt.Sprint(a) == fmt.Sprint(b)
}

// toInt coerces a decoded numeric value into an int, via numeric.ToFloat64.
func toInt(v any) (int, bool) {
	f, ok := numeric.ToFloat64(v)
	if !ok {
		return 0, false
	}
	return int(f), true
}
