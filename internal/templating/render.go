package templating

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
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
