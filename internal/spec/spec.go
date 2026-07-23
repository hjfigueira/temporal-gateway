// Package spec loads the API specification: an OpenAPI 3.0 document whose
// operations carry an "x-temporal" extension listing one or more Temporal
// actions the HTTP operation maps to (start/signal/query a workflow, etc),
// so a single call can dispatch to multiple workflows. The gateway uses this
// in-memory representation as the basis for generating routes at runtime.
// Before parsing, the raw file is run through envsubst.Expand, so values may
// reference "${VAR}" or "${VAR:-default}" environment variables.
package spec

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"temporal-gateway/internal/envsubst"
)

// TemporalAction identifies which Temporal client call an operation
// translates into.
type TemporalAction string

const (
	ActionStartWorkflow     TemporalAction = "startWorkflow"
	ActionSignalWorkflow    TemporalAction = "signalWorkflow"
	ActionQueryWorkflow     TemporalAction = "queryWorkflow"
	ActionCancelWorkflow    TemporalAction = "cancelWorkflow"
	ActionTerminateWorkflow TemporalAction = "terminateWorkflow"
	ActionGetResult         TemporalAction = "getResult"
)

// TemporalBinding is the "x-temporal" vendor extension attached to an
// operation. Which fields are meaningful depends on Action, e.g. TaskQueue
// and WorkflowType only apply to ActionStartWorkflow.
//
// The fields below WorkflowID mirror go.temporal.io/sdk/client's
// StartWorkflowOptions and apply only to ActionStartWorkflow. Durations are
// strings parsed with time.ParseDuration (e.g. "30s", "5m"). One
// StartWorkflowOptions field is deliberately not exposed here:
// VersioningOverride, an interface type for worker-deployment versioning
// that has no simple scalar/map representation. SearchAttributes uses the
// SDK's typed search attribute API (client.StartWorkflowOptions.
// SearchAttributes, an untyped map, is deprecated in favor of
// TypedSearchAttributes).
type TemporalBinding struct {
	Action       TemporalAction `yaml:"action"`
	TaskQueue    string         `yaml:"taskQueue,omitempty"`
	WorkflowType string         `yaml:"workflowType,omitempty"`
	WorkflowID   string         `yaml:"workflowId,omitempty"`
	SignalName   string         `yaml:"signalName,omitempty"`
	QueryType    string         `yaml:"queryType,omitempty"`
	// IDReusePolicy additionally accepts "TerminateIfRunning": terminate the
	// current run if one is already running, otherwise allow reuse of the
	// ID. The underlying Temporal enum for this is deprecated, so the
	// dispatcher implements it via the documented replacement instead -
	// WorkflowIDReusePolicy AllowDuplicate combined with
	// WorkflowIDConflictPolicy TerminateExisting - which is why it can't be
	// combined with an explicit WorkflowIDConflictPolicy below.
	IDReusePolicy string `yaml:"idReusePolicy,omitempty"`

	WorkflowExecutionTimeout                 string            `yaml:"workflowExecutionTimeout,omitempty"`
	WorkflowRunTimeout                       string            `yaml:"workflowRunTimeout,omitempty"`
	WorkflowTaskTimeout                      string            `yaml:"workflowTaskTimeout,omitempty"`
	WorkflowIDConflictPolicy                 string            `yaml:"workflowIdConflictPolicy,omitempty"`
	WorkflowExecutionErrorWhenAlreadyStarted bool              `yaml:"workflowExecutionErrorWhenAlreadyStarted,omitempty"`
	RetryPolicy                              *RetryPolicy      `yaml:"retryPolicy,omitempty"`
	CronSchedule                             string            `yaml:"cronSchedule,omitempty"`
	Memo                                     map[string]any    `yaml:"memo,omitempty"`
	SearchAttributes                         []SearchAttribute `yaml:"searchAttributes,omitempty"`
	EnableEagerStart                         bool              `yaml:"enableEagerStart,omitempty"`
	StartDelay                               string            `yaml:"startDelay,omitempty"`
	StaticSummary                            string            `yaml:"staticSummary,omitempty"`
	StaticDetails                            string            `yaml:"staticDetails,omitempty"`
	Priority                                 *Priority         `yaml:"priority,omitempty"`
}

// SearchAttribute is one typed search attribute to set on a started
// workflow. Unlike a plain map, each entry must declare its Type: Temporal's
// typed search attribute API (go.temporal.io/sdk/temporal's
// SearchAttributeKeyString, SearchAttributeKeyKeyword, etc.) keys each
// attribute by name *and* type, and the server must have that name
// registered with a matching type. Type is one of "string", "keyword",
// "bool", "int", "float", "time" (an RFC3339 string), or "keywordList" (a
// list of strings); Value's shape must match Type.
type SearchAttribute struct {
	Name  string `yaml:"name"`
	Type  string `yaml:"type"`
	Value any    `yaml:"value"`
}

// RetryPolicy mirrors go.temporal.io/sdk/temporal.RetryPolicy.
type RetryPolicy struct {
	InitialInterval        string   `yaml:"initialInterval,omitempty"`
	BackoffCoefficient     float64  `yaml:"backoffCoefficient,omitempty"`
	MaximumInterval        string   `yaml:"maximumInterval,omitempty"`
	MaximumAttempts        int32    `yaml:"maximumAttempts,omitempty"`
	NonRetryableErrorTypes []string `yaml:"nonRetryableErrorTypes,omitempty"`
}

// Priority mirrors go.temporal.io/sdk/temporal.Priority.
type Priority struct {
	PriorityKey    int     `yaml:"priorityKey,omitempty"`
	FairnessKey    string  `yaml:"fairnessKey,omitempty"`
	FairnessWeight float32 `yaml:"fairnessWeight,omitempty"`
}

type Parameter struct {
	Name     string         `yaml:"name"`
	In       string         `yaml:"in"`
	Required bool           `yaml:"required,omitempty"`
	Schema   map[string]any `yaml:"schema,omitempty"`
}

type MediaType struct {
	Schema map[string]any `yaml:"schema,omitempty"`
}

type RequestBody struct {
	Required bool                 `yaml:"required,omitempty"`
	Content  map[string]MediaType `yaml:"content,omitempty"`
}

type Operation struct {
	OperationID string            `yaml:"operationId"`
	Summary     string            `yaml:"summary,omitempty"`
	Parameters  []Parameter       `yaml:"parameters,omitempty"`
	RequestBody *RequestBody      `yaml:"requestBody,omitempty"`
	Responses   map[string]any    `yaml:"responses,omitempty"`
	Temporal    []TemporalBinding `yaml:"x-temporal"`
}

type PathItem struct {
	Get    *Operation `yaml:"get,omitempty"`
	Post   *Operation `yaml:"post,omitempty"`
	Put    *Operation `yaml:"put,omitempty"`
	Patch  *Operation `yaml:"patch,omitempty"`
	Delete *Operation `yaml:"delete,omitempty"`
}

type Info struct {
	Title       string `yaml:"title"`
	Version     string `yaml:"version"`
	Description string `yaml:"description,omitempty"`
}

// Spec is the in-memory representation of the loaded API specification.
type Spec struct {
	OpenAPI string              `yaml:"openapi"`
	Info    Info                `yaml:"info"`
	Paths   map[string]PathItem `yaml:"paths"`
}

// Load reads and parses a Spec from path.
func Load(path string) (*Spec, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("spec: read %q: %w", path, err)
	}

	data, err = envsubst.Expand(data)
	if err != nil {
		return nil, fmt.Errorf("spec: %q: %w", path, err)
	}

	var s Spec
	if err := yaml.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("spec: parse %q: %w", path, err)
	}

	if err := s.validate(); err != nil {
		return nil, fmt.Errorf("spec: %q: %w", path, err)
	}

	return &s, nil
}

// validIDReusePolicies mirrors the WorkflowIDReusePolicy values the
// dispatcher understands (see internal/temporal.parseIDReusePolicy). Kept
// here, rather than importing the Temporal SDK, so the spec package stays
// free of transport-specific dependencies.
var validIDReusePolicies = map[string]bool{
	"":                         true,
	"AllowDuplicate":           true,
	"AllowDuplicateFailedOnly": true,
	"RejectDuplicate":          true,
	"TerminateIfRunning":       true,
}

// validIDConflictPolicies mirrors the WorkflowIDConflictPolicy values the
// dispatcher understands (see internal/temporal.parseIDConflictPolicy).
var validIDConflictPolicies = map[string]bool{
	"":                  true,
	"Fail":              true,
	"UseExisting":       true,
	"TerminateExisting": true,
}

// validSearchAttributeTypes mirrors the SearchAttribute.Type values the
// dispatcher understands (see internal/temporal.searchAttributeUpdate),
// each corresponding to one of the SDK's typed search attribute key
// constructors.
var validSearchAttributeTypes = map[string]bool{
	"string":      true,
	"keyword":     true,
	"bool":        true,
	"int":         true,
	"float":       true,
	"time":        true,
	"keywordList": true,
}

// validateSearchAttributeValue checks that sa.Value's shape matches its
// declared Type, so a mismatch is caught at spec-load time rather than
// surfacing as a dispatch-time error on the first request. Assumes
// validSearchAttributeTypes[sa.Type] is already true.
func validateSearchAttributeValue(sa SearchAttribute) error {
	switch sa.Type {
	case "string", "keyword":
		if _, ok := sa.Value.(string); !ok {
			return fmt.Errorf("value must be a string for type %q", sa.Type)
		}
	case "bool":
		if _, ok := sa.Value.(bool); !ok {
			return fmt.Errorf("value must be a boolean for type %q", sa.Type)
		}
	case "int":
		switch sa.Value.(type) {
		case int, int64:
		default:
			return fmt.Errorf("value must be an integer for type %q", sa.Type)
		}
	case "float":
		switch sa.Value.(type) {
		case float64, float32, int, int64:
		default:
			return fmt.Errorf("value must be a number for type %q", sa.Type)
		}
	case "time":
		s, ok := sa.Value.(string)
		if !ok {
			return fmt.Errorf("value must be an RFC3339 string for type %q", sa.Type)
		}
		if _, err := time.Parse(time.RFC3339, s); err != nil {
			return fmt.Errorf("value %q is not a valid RFC3339 timestamp: %w", s, err)
		}
	case "keywordList":
		list, ok := sa.Value.([]any)
		if !ok {
			return fmt.Errorf("value must be a list of strings for type %q", sa.Type)
		}
		for _, v := range list {
			if _, ok := v.(string); !ok {
				return fmt.Errorf("value must be a list of strings for type %q", sa.Type)
			}
		}
	}
	return nil
}

// validate checks every operation's x-temporal bindings and returns a
// single error joining every problem found (via errors.Error(), one per
// line), rather than stopping at the first, so a misconfigured spec can be
// fixed in one pass instead of being rediscovered error-by-error.
func (s *Spec) validate() error {
	var errs []error
	for path, item := range s.Paths {
		for method, op := range item.operations() {
			if len(op.Temporal) == 0 {
				errs = append(errs, fmt.Errorf("%s %s: missing x-temporal", method, path))
				continue
			}
			for i, t := range op.Temporal {
				errs = append(errs, validateBinding(method, path, i, t)...)
			}
		}
	}
	return errors.Join(errs...)
}

func validateBinding(method, path string, i int, t TemporalBinding) []error {
	var errs []error

	if t.Action == "" {
		errs = append(errs, fmt.Errorf("%s %s: x-temporal[%d]: missing action", method, path, i))
	}

	switch t.Action {
	case ActionStartWorkflow:
		if t.WorkflowType == "" {
			errs = append(errs, fmt.Errorf("%s %s: x-temporal[%d]: startWorkflow requires workflowType", method, path, i))
		}
		if t.TaskQueue == "" {
			errs = append(errs, fmt.Errorf("%s %s: x-temporal[%d]: startWorkflow requires taskQueue", method, path, i))
		}
		if !validIDReusePolicies[t.IDReusePolicy] {
			errs = append(errs, fmt.Errorf("%s %s: x-temporal[%d]: unknown idReusePolicy %q", method, path, i, t.IDReusePolicy))
		}
		if !validIDConflictPolicies[t.WorkflowIDConflictPolicy] {
			errs = append(errs, fmt.Errorf("%s %s: x-temporal[%d]: unknown workflowIdConflictPolicy %q", method, path, i, t.WorkflowIDConflictPolicy))
		}
		if t.IDReusePolicy == "TerminateIfRunning" && t.WorkflowIDConflictPolicy != "" {
			errs = append(errs, fmt.Errorf("%s %s: x-temporal[%d]: idReusePolicy TerminateIfRunning cannot be combined with workflowIdConflictPolicy (TerminateIfRunning already implies TerminateExisting)", method, path, i))
		}
		if t.CronSchedule != "" && t.StartDelay != "" {
			errs = append(errs, fmt.Errorf("%s %s: x-temporal[%d]: cronSchedule and startDelay cannot both be set", method, path, i))
		}
		durations := []struct{ name, value string }{
			{"workflowExecutionTimeout", t.WorkflowExecutionTimeout},
			{"workflowRunTimeout", t.WorkflowRunTimeout},
			{"workflowTaskTimeout", t.WorkflowTaskTimeout},
			{"startDelay", t.StartDelay},
		}
		if t.RetryPolicy != nil {
			durations = append(durations,
				struct{ name, value string }{"retryPolicy.initialInterval", t.RetryPolicy.InitialInterval},
				struct{ name, value string }{"retryPolicy.maximumInterval", t.RetryPolicy.MaximumInterval},
			)
		}
		for _, d := range durations {
			if d.value == "" {
				continue
			}
			if _, err := time.ParseDuration(d.value); err != nil {
				errs = append(errs, fmt.Errorf("%s %s: x-temporal[%d]: invalid %s %q: %w", method, path, i, d.name, d.value, err))
			}
		}
		for j, sa := range t.SearchAttributes {
			if sa.Name == "" {
				errs = append(errs, fmt.Errorf("%s %s: x-temporal[%d]: searchAttributes[%d]: missing name", method, path, i, j))
			}
			if !validSearchAttributeTypes[sa.Type] {
				errs = append(errs, fmt.Errorf("%s %s: x-temporal[%d]: searchAttributes[%d]: unknown type %q", method, path, i, j, sa.Type))
				continue
			}
			if err := validateSearchAttributeValue(sa); err != nil {
				errs = append(errs, fmt.Errorf("%s %s: x-temporal[%d]: searchAttributes[%d] %q: %w", method, path, i, j, sa.Name, err))
			}
		}
	case ActionSignalWorkflow:
		if t.SignalName == "" {
			errs = append(errs, fmt.Errorf("%s %s: x-temporal[%d]: signalWorkflow requires signalName", method, path, i))
		}
	case ActionQueryWorkflow:
		if t.QueryType == "" {
			errs = append(errs, fmt.Errorf("%s %s: x-temporal[%d]: queryWorkflow requires queryType", method, path, i))
		}
	case ActionCancelWorkflow, ActionTerminateWorkflow, ActionGetResult:
		// no action-specific required fields beyond workflowId.
	case "":
		// already reported above as "missing action".
	default:
		errs = append(errs, fmt.Errorf("%s %s: x-temporal[%d]: unknown action %q", method, path, i, t.Action))
	}

	if t.WorkflowID == "" {
		errs = append(errs, fmt.Errorf("%s %s: x-temporal[%d]: missing workflowId", method, path, i))
	}

	return errs
}

// Route is a flattened (method, path, operation) triple, convenient for
// iterating over the spec when registering HTTP handlers.
type Route struct {
	Method    string
	Path      string
	Operation *Operation
}

// Routes flattens the spec's paths into a stable, sorted list of routes.
func (s *Spec) Routes() []Route {
	routes := make([]Route, 0, len(s.Paths))
	for path, item := range s.Paths {
		for method, op := range item.operations() {
			routes = append(routes, Route{Method: method, Path: path, Operation: op})
		}
	}
	sort.Slice(routes, func(i, j int) bool {
		if routes[i].Path != routes[j].Path {
			return routes[i].Path < routes[j].Path
		}
		return routes[i].Method < routes[j].Method
	})
	return routes
}

func (p PathItem) operations() map[string]*Operation {
	ops := map[string]*Operation{}
	if p.Get != nil {
		ops["GET"] = p.Get
	}
	if p.Post != nil {
		ops["POST"] = p.Post
	}
	if p.Put != nil {
		ops["PUT"] = p.Put
	}
	if p.Patch != nil {
		ops["PATCH"] = p.Patch
	}
	if p.Delete != nil {
		ops["DELETE"] = p.Delete
	}
	return ops
}

// PathParamNames returns the {curly-brace} placeholder names declared in a
// path template, e.g. "/orders/{orderId}" -> ["orderId"].
func PathParamNames(path string) []string {
	var names []string
	for {
		start := strings.IndexByte(path, '{')
		if start == -1 {
			break
		}
		end := strings.IndexByte(path[start:], '}')
		if end == -1 {
			break
		}
		names = append(names, path[start+1:start+end])
		path = path[start+end+1:]
	}
	return names
}
