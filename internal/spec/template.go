package spec

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// TemplatePlaceholder matches a "{name}" placeholder in a workflowId
// template. Shared with internal/gateway, which renders the same templates
// at request time.
var TemplatePlaceholder = regexp.MustCompile(`\{([^{}]+)}`)

// validateWorkflowIDTemplate checks every placeholder in a trigger's
// workflowId: each must be "uuidv7" or "origin.field" with a known origin,
// and a "path.X" must name one of the route's own path parameters. A
// placeholder that can never resolve would otherwise fail every request
// that hits the route (see ADR-020).
func validateWorkflowIDTemplate(method, path string, i int, tmpl string) []error {
	var errs []error
	for _, m := range TemplatePlaceholder.FindAllStringSubmatch(tmpl, -1) {
		name := m[1]
		if strings.EqualFold(name, "uuidv7") {
			continue
		}
		origin, field, ok := strings.Cut(name, ".")
		if !ok || field == "" {
			errs = append(errs, fmt.Errorf("%s %s: x-temporal.triggers[%d]: workflowId placeholder {%s} must be {uuidv7} or {origin.field}", method, path, i, name))
			continue
		}
		switch strings.ToLower(origin) {
		case "path":
			if !slices.Contains(PathParamNames(path), field) {
				errs = append(errs, fmt.Errorf("%s %s: x-temporal.triggers[%d]: workflowId placeholder {%s} names no path parameter of this route", method, path, i, name))
			}
		case "body", "query", "header":
		default:
			errs = append(errs, fmt.Errorf("%s %s: x-temporal.triggers[%d]: workflowId placeholder {%s} has unknown origin %q (want path, body, query, or header)", method, path, i, name, origin))
		}
	}
	return errs
}
