// Package numeric coerces the numeric types a YAML/JSON decode can hand
// back (float64, float32, int, int64 - depending on the source and the
// literal's own shape) into a single concrete type, so callers don't each
// repeat the same type switch. Shared by internal/validate (JSON Schema
// minimum/maximum, enum comparison) and internal/temporal (search
// attribute values), which have no other reason to depend on each other.
package numeric

// ToFloat64 coerces a decoded numeric value into a float64. Handles both
// encoding/json's float64 and the int/int64/float32 variants a caller might
// otherwise pass in (e.g. from a YAML-decoded literal, which gopkg.in/
// yaml.v3 decodes as int when written without a decimal point even if the
// schema calls for a float).
func ToFloat64(v any) (float64, bool) {
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
