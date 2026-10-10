// Package templating parses and renders the "{...}" placeholders in an
// x-temporal workflowId template (see ADR-004, ADR-020, ADR-025):
// "{origin.field}" request references (body paths may nest, e.g.
// "{body.items[2].sku}"), "{uuidv7}", and "{fingerprint(ref)}". It has no
// dependency on the spec or gateway packages: spec validates templates
// with ParsePlaceholder at load time, and gateway renders them per request
// with Render.
package templating

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// placeholderPattern matches one "{...}" placeholder; group 1 is the text
// between the braces.
var placeholderPattern = regexp.MustCompile(`\{([^{}]+)}`)

// Placeholders returns the text inside each "{...}" placeholder in tmpl, in
// order, e.g. "order-{path.id}-{uuidv7}" -> ["path.id", "uuidv7"].
func Placeholders(tmpl string) []string {
	var names []string
	for _, m := range placeholderPattern.FindAllStringSubmatch(tmpl, -1) {
		names = append(names, m[1])
	}
	return names
}

// PlaceholderKind is what a parsed placeholder renders to.
type PlaceholderKind int

const (
	// PlaceholderField renders the referenced request value itself.
	PlaceholderField PlaceholderKind = iota
	// PlaceholderUUIDv7 renders a fresh UUIDv7 per occurrence.
	PlaceholderUUIDv7
	// PlaceholderFingerprint renders a short, stable hash of the
	// referenced request value (see ADR-025).
	PlaceholderFingerprint
)

// Placeholder is one parsed "{...}" from a workflowId template.
type Placeholder struct {
	Kind PlaceholderKind
	Ref  Reference // unused for PlaceholderUUIDv7
}

// Reference names a value in the incoming request: Origin is "path",
// "query", "header", or "body". Path/query/header values are looked up by
// Name, taken literally. Body values are reached through Path; an empty
// Path means the whole body.
type Reference struct {
	Origin string
	Name   string
	Path   []PathSegment
}

// PathSegment is one step into a JSON body: an object key, or an array
// index when IsIndex is set.
type PathSegment struct {
	Key     string
	Index   int
	IsIndex bool
}

// ParsePlaceholder parses the text between a placeholder's braces:
// "uuidv7", "origin.field" (body fields may go deeper, e.g.
// "body.items[2].sku"), or "fingerprint(ref)" where ref is any such
// reference or the whole "body". Keywords and origins are case-insensitive.
func ParsePlaceholder(s string) (Placeholder, error) {
	if strings.EqualFold(s, "uuidv7") {
		return Placeholder{Kind: PlaceholderUUIDv7}, nil
	}
	if len(s) > len("fingerprint(") && strings.EqualFold(s[:len("fingerprint(")], "fingerprint(") {
		inner, ok := strings.CutSuffix(s[len("fingerprint("):], ")")
		if !ok {
			return Placeholder{}, fmt.Errorf("{%s} is missing its closing parenthesis", s)
		}
		ref, err := parseReference(inner, true)
		if err != nil {
			return Placeholder{}, err
		}
		return Placeholder{Kind: PlaceholderFingerprint, Ref: ref}, nil
	}
	ref, err := parseReference(s, false)
	if err != nil {
		return Placeholder{}, err
	}
	return Placeholder{Kind: PlaceholderField, Ref: ref}, nil
}

// parseReference parses "origin.field" or a body path. wholeBody allows a
// bare "body" (only meaningful inside fingerprint(...): a whole JSON
// document isn't a usable workflow ID component on its own).
func parseReference(s string, wholeBody bool) (Reference, error) {
	end := strings.IndexAny(s, ".[")
	if end == -1 {
		end = len(s)
	}
	origin, rest := strings.ToLower(s[:end]), s[end:]

	switch origin {
	case "body":
		if rest == "" && !wholeBody {
			return Reference{}, fmt.Errorf("{%s} would embed the whole body; use {fingerprint(%s)} or a field like {body.id}", s, s)
		}
		path, err := parseBodyPath(rest)
		if err != nil {
			return Reference{}, fmt.Errorf("{%s}: %w", s, err)
		}
		return Reference{Origin: origin, Path: path}, nil
	case "path", "query", "header":
		name, ok := strings.CutPrefix(rest, ".")
		if !ok || name == "" {
			return Reference{}, fmt.Errorf("{%s} must be {uuidv7}, {origin.field}, or {fingerprint(...)}", s)
		}
		return Reference{Origin: origin, Name: name}, nil
	case "":
		return Reference{}, fmt.Errorf("{%s} must be {uuidv7}, {origin.field}, or {fingerprint(...)}", s)
	default:
		if rest == "" {
			return Reference{}, fmt.Errorf("{%s} must be {uuidv7}, {origin.field}, or {fingerprint(...)}", s)
		}
		return Reference{}, fmt.Errorf("{%s} has unknown origin %q (want path, body, query, or header)", s, s[:end])
	}
}

// parseBodyPath parses the part of a body reference after "body", e.g.
// ".items[2].sku" -> [items, 2, sku].
func parseBodyPath(s string) ([]PathSegment, error) {
	var path []PathSegment
	for s != "" {
		switch s[0] {
		case '.':
			end := strings.IndexAny(s[1:], ".[")
			if end == -1 {
				end = len(s) - 1
			}
			key := s[1 : end+1]
			if key == "" {
				return nil, fmt.Errorf("empty field name")
			}
			path = append(path, PathSegment{Key: key})
			s = s[end+1:]
		case '[':
			end := strings.IndexByte(s, ']')
			if end == -1 {
				return nil, fmt.Errorf("unclosed '['")
			}
			index, err := strconv.Atoi(s[1:end])
			if err != nil || index < 0 {
				return nil, fmt.Errorf("index %q is not a non-negative integer", s[1:end])
			}
			path = append(path, PathSegment{Index: index, IsIndex: true})
			s = s[end+1:]
		default:
			return nil, fmt.Errorf("expected '.' or '[' at %q", s)
		}
	}
	return path, nil
}
