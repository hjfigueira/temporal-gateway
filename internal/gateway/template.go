package gateway

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/google/uuid"

	"temporal-gateway/internal/spec"
)

// renderTemplate substitutes "{name}" placeholders in tmpl using resolve,
// e.g. renderTemplate("order-{path.orderId}", resolve) -> "order-o1" when
// resolve("path.orderId") returns ("o1", true). It also returns every
// placeholder resolve couldn't satisfy, so the caller can reject the
// request instead of dispatching a literal "{...}" workflow ID that every
// such request would share (see ADR-020).
func renderTemplate(tmpl string, resolve func(name string) (string, bool)) (string, []string) {
	var unresolved []string
	rendered := spec.TemplatePlaceholder.ReplaceAllStringFunc(tmpl, func(match string) string {
		name := match[1 : len(match)-1]
		if value, ok := resolve(name); ok {
			return value
		}
		unresolved = append(unresolved, match)
		return match
	})
	return rendered, unresolved
}

// fieldResolver returns a lookup used to fill "{name}" placeholders in a
// workflowId template; see spec.ParsePlaceholder for the syntax. A
// reference the request doesn't satisfy (missing field, index out of
// range, no body) reports false, so the request is rejected (ADR-020).
func fieldResolver(r *http.Request, pathParams map[string]string, body any) func(name string) (string, bool) {
	query := r.URL.Query()

	lookup := func(ref spec.Reference) (any, bool) {
		switch ref.Origin {
		case "path":
			value, ok := pathParams[ref.Name]
			return value, ok
		case "query":
			values := query[ref.Name]
			if len(values) == 0 {
				return nil, false
			}
			return values[0], true
		case "header":
			values := r.Header[http.CanonicalHeaderKey(ref.Name)]
			if len(values) == 0 {
				return nil, false
			}
			return values[0], true
		default: // "body"; ParsePlaceholder admits no other origin
			if body == nil {
				return nil, false
			}
			return walkBody(body, ref.Path)
		}
	}

	return func(name string) (string, bool) {
		// The spec was validated at startup, so a parse error can't happen
		// here; treat it as unresolved anyway rather than guess.
		p, err := spec.ParsePlaceholder(name)
		if err != nil {
			return "", false
		}
		if p.Kind == spec.PlaceholderUUIDv7 {
			// NewV7 only fails if crypto/rand does, which crashes the
			// process anyway since Go 1.24.
			return uuid.Must(uuid.NewV7()).String(), true
		}
		value, ok := lookup(p.Ref)
		if !ok {
			return "", false
		}
		if p.Kind == spec.PlaceholderFingerprint {
			return fingerprint(value), true
		}
		return stringifyField(value), true
	}
}

// walkBody follows path into a decoded JSON body.
func walkBody(value any, path []spec.PathSegment) (any, bool) {
	for _, seg := range path {
		if seg.IsIndex {
			list, ok := value.([]any)
			if !ok || seg.Index >= len(list) {
				return nil, false
			}
			value = list[seg.Index]
			continue
		}
		obj, ok := value.(map[string]any)
		if !ok {
			return nil, false
		}
		if value, ok = obj[seg.Key]; !ok {
			return nil, false
		}
	}
	return value, true
}

// fingerprint returns the first 16 hex chars (64 bits) of the SHA-256 of
// value's canonical JSON. encoding/json sorts object keys and a decoded
// number is always a float64, so key order and 1 vs 1.0 don't change it.
func fingerprint(value any) string {
	// value is a decoded JSON value or a string, which always re-encode.
	data, _ := json.Marshal(value)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:8])
}

// stringifyField renders a decoded JSON body value as a string for use in a
// workflowId template. JSON objects and arrays are not meaningful workflow
// ID components, so they render as their Go-syntax representation rather
// than being rejected outright.
func stringifyField(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case nil:
		return ""
	default:
		return fmt.Sprint(t)
	}
}
