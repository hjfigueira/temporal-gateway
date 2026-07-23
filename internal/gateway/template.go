package gateway

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/google/uuid"
)

// templatePlaceholder matches a "{name}" placeholder in a workflowId
// template.
var templatePlaceholder = regexp.MustCompile(`\{([^{}]+)\}`)

// renderTemplate substitutes "{name}" placeholders in tmpl using resolve,
// e.g. renderTemplate("order-{path.orderId}", resolve) -> "order-o1" when
// resolve("path.orderId") returns ("o1", true). A placeholder resolve can't
// satisfy is left untouched.
func renderTemplate(tmpl string, resolve func(name string) (string, bool)) string {
	return templatePlaceholder.ReplaceAllStringFunc(tmpl, func(match string) string {
		name := match[1 : len(match)-1]
		if value, ok := resolve(name); ok {
			return value
		}
		return match
	})
}

// fieldResolver returns a lookup used to fill "{name}" placeholders in a
// workflowId template. name is either the reserved "uuidv7" keyword, which
// generates a fresh UUIDv7 per occurrence, or an "origin.field" reference
// naming which part of the request field comes from: "path", "body",
// "query", or "header".
func fieldResolver(r *http.Request, pathParams map[string]string, body any) func(name string) (string, bool) {
	bodyFields, _ := body.(map[string]any)
	query := r.URL.Query()

	return func(name string) (string, bool) {
		if strings.EqualFold(name, "uuidv7") {
			id, err := uuid.NewV7()
			if err != nil {
				return "", false
			}
			return id.String(), true
		}

		origin, field, ok := strings.Cut(name, ".")
		if !ok {
			return "", false
		}

		switch strings.ToLower(origin) {
		case "path":
			value, ok := pathParams[field]
			return value, ok
		case "body":
			value, ok := bodyFields[field]
			if !ok {
				return "", false
			}
			return stringifyField(value), true
		case "query":
			values, ok := query[field]
			if !ok || len(values) == 0 {
				return "", false
			}
			return values[0], true
		case "header":
			values, ok := r.Header[http.CanonicalHeaderKey(field)]
			if !ok || len(values) == 0 {
				return "", false
			}
			return values[0], true
		default:
			return "", false
		}
	}
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
