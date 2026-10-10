package templating

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"

	"github.com/google/uuid"
)

// Source is the request data placeholders resolve against.
type Source struct {
	PathParams map[string]string
	Query      url.Values
	Header     http.Header
	// Body is the decoded JSON body, or nil when the request had none.
	Body any
}

// Render substitutes every placeholder in tmpl from src, e.g.
// "order-{path.orderId}" -> "order-o1". It also returns every placeholder
// src couldn't satisfy, left as-is in the result, so the caller can reject
// the request instead of using a literal "{...}" workflow ID that every
// such request would share (see ADR-020).
func Render(tmpl string, src Source) (string, []string) {
	return render(tmpl, src.resolve)
}

func render(tmpl string, resolve func(name string) (string, bool)) (string, []string) {
	var unresolved []string
	rendered := placeholderPattern.ReplaceAllStringFunc(tmpl, func(match string) string {
		name := match[1 : len(match)-1]
		if value, ok := resolve(name); ok {
			return value
		}
		unresolved = append(unresolved, match)
		return match
	})
	return rendered, unresolved
}

// resolve renders one placeholder's text (see ParsePlaceholder). A
// reference src doesn't satisfy (missing field, index out of range, no
// body) reports false.
func (src Source) resolve(name string) (string, bool) {
	// Specs are validated at load time, so a parse error can't happen for
	// a served route; treat it as unresolved anyway rather than guess.
	p, err := ParsePlaceholder(name)
	if err != nil {
		return "", false
	}
	if p.Kind == PlaceholderUUIDv7 {
		// NewV7 only fails if crypto/rand does, which crashes the process
		// anyway since Go 1.24.
		return uuid.Must(uuid.NewV7()).String(), true
	}
	value, ok := src.lookup(p.Ref)
	if !ok {
		return "", false
	}
	if p.Kind == PlaceholderFingerprint {
		return fingerprint(value), true
	}
	return stringifyField(value), true
}

// lookup finds ref's value in src.
func (src Source) lookup(ref Reference) (any, bool) {
	switch ref.Origin {
	case "path":
		value, ok := src.PathParams[ref.Name]
		return value, ok
	case "query":
		values := src.Query[ref.Name]
		if len(values) == 0 {
			return nil, false
		}
		return values[0], true
	case "header":
		values := src.Header[http.CanonicalHeaderKey(ref.Name)]
		if len(values) == 0 {
			return nil, false
		}
		return values[0], true
	default: // "body"; ParsePlaceholder admits no other origin
		if src.Body == nil {
			return nil, false
		}
		return walkBody(src.Body, ref.Path)
	}
}

// walkBody follows path into a decoded JSON body.
func walkBody(value any, path []PathSegment) (any, bool) {
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
// value's canonical JSON. encoding/json sorts object keys, and numbers are
// hashed as float64, so key order and 1 vs 1.0 don't change it. The format
// is a contract (ADR-025): changing it changes every derived workflow ID.
func fingerprint(value any) string {
	// value is a decoded JSON value or a string, which always re-encode.
	data, _ := json.Marshal(asFloats(value))
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:8])
}

// asFloats returns value with every json.Number converted to float64, the
// form fingerprints were defined over before bodies kept exact numbers
// (ADR-028).
func asFloats(value any) any {
	switch v := value.(type) {
	case json.Number:
		f, _ := v.Float64()
		return f
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, e := range v {
			out[k] = asFloats(e)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, e := range v {
			out[i] = asFloats(e)
		}
		return out
	default:
		return v
	}
}

// stringifyField renders a decoded JSON body value as a string for use in a
// workflowId template. A number keeps its exact digits as sent (ADR-028);
// an object or array renders as its compact JSON.
func stringifyField(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case json.Number:
		return t.String()
	case nil:
		return ""
	default:
		data, _ := json.Marshal(t)
		return string(data)
	}
}
