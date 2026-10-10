package gateway

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"
)

// bodyField keys a violation that applies to the request body as a whole,
// e.g. a required body that's missing.
const bodyField = "_body"

// validationOptions configures kin-openapi's request validation (ADR-029).
var validationOptions = &openapi3filter.Options{
	// Report every problem at once, not just the first.
	MultiError: true,
	// Schema defaults would be written into the request; the dispatched
	// body must stay exactly what the client sent.
	SkipSettingDefaults: true,
	// The spec's security schemes are not enforced by the gateway (ADR-015).
	AuthenticationFunc: openapi3filter.NoopAuthenticationFunc,
}

// validateRequest validates r's parameters and body (already read into
// body) against its OpenAPI operation, returning every violation keyed by
// field, or nil when the request is valid. A route without an OpenAPI
// operation (one not built by spec.Load) has nothing to check.
func validateRequest(ctx context.Context, route *routers.Route, r *http.Request, body []byte, pathParams map[string]string) map[string][]string {
	if route == nil {
		return nil
	}
	req := r.Clone(ctx)
	req.Body = io.NopCloser(bytes.NewReader(body))
	req.ContentLength = int64(len(body))
	req.GetBody = nil
	// The gateway only speaks JSON, so a body without a Content-Type is
	// treated as JSON rather than rejected.
	if len(body) > 0 && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}

	err := openapi3filter.ValidateRequest(ctx, &openapi3filter.RequestValidationInput{
		Request:    req,
		PathParams: pathParams,
		Route:      route,
		Options:    validationOptions,
	})
	if err == nil {
		return nil
	}
	fields := map[string][]string{}
	collectViolations(err, "", fields)
	return fields
}

// collectViolations flattens kin-openapi's error tree into fields. field is
// the parameter being reported ("query.limit"), or "" inside the request
// body, where each schema error names its own field.
func collectViolations(err error, field string, fields map[string][]string) {
	switch e := err.(type) {
	case openapi3.MultiError:
		for _, inner := range e {
			collectViolations(inner, field, fields)
		}
	case *openapi3filter.RequestError:
		if e.Parameter != nil {
			field = e.Parameter.In + "." + e.Parameter.Name
		}
		switch e.Err.(type) {
		case openapi3.MultiError, *openapi3.SchemaError:
			collectViolations(e.Err, field, fields)
		default:
			addViolation(fields, field, requestReason(e))
		}
	case *openapi3.SchemaError:
		if field == "" {
			field = pointerField(e.JSONPointer())
		}
		addViolation(fields, field, e.Reason)
	default:
		addViolation(fields, field, err.Error())
	}
}

func addViolation(fields map[string][]string, field, message string) {
	if field == "" {
		field = bodyField
	}
	fields[field] = append(fields[field], message)
}

// requestReason is RequestError.Error() without its "request body has an
// error:" / "parameter ... has an error:" prefix, which the field key
// already conveys.
func requestReason(e *openapi3filter.RequestError) string {
	switch {
	case e.Err == nil:
		return e.Reason
	case e.Reason == "" || e.Reason == e.Err.Error():
		return e.Err.Error()
	default:
		return e.Reason + ": " + e.Err.Error()
	}
}

// pointerField renders a JSON pointer as a field path, e.g.
// ["items", "1", "sku"] -> "items[1].sku".
func pointerField(pointer []string) string {
	var b strings.Builder
	for _, seg := range pointer {
		if seg != "" && strings.Trim(seg, "0123456789") == "" {
			b.WriteString("[" + seg + "]")
			continue
		}
		if b.Len() > 0 {
			b.WriteByte('.')
		}
		b.WriteString(seg)
	}
	return b.String()
}
