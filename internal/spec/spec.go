// Package spec loads the API specification: an OpenAPI 3.0 document whose
// operations carry an "x-temporal" extension listing one or more Temporal
// actions the HTTP operation maps to (start/signal/query a workflow, etc),
// so a single call can dispatch to multiple workflows. The gateway uses this
// in-memory representation as the basis for generating routes at runtime.
// Before parsing, the raw file is run through envsubst.Expand, so values may
// reference "${VAR}" or "${VAR:-default}" environment variables.
package spec

import (
	"fmt"
	"os"

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
	Action TemporalAction `yaml:"action"`
	// Namespace selects which of the gateway's configured Temporal
	// connections (temporal.connections in config.yml) this binding
	// dispatches against - see internal/temporal.Connections. Required on
	// every binding, regardless of Action.
	Namespace    string `yaml:"namespace"`
	TaskQueue    string `yaml:"taskQueue,omitempty"`
	WorkflowType string `yaml:"workflowType,omitempty"`
	WorkflowID   string `yaml:"workflowId,omitempty"`
	SignalName   string `yaml:"signalName,omitempty"`
	QueryType    string `yaml:"queryType,omitempty"`
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

// Parameter is an OpenAPI parameter declaration (path, query, or header).
// The gateway only reads Name/In itself (see PathParamNames); Required and
// Schema are carried through for documentation purposes only and are not
// currently enforced.
type Parameter struct {
	Name     string         `yaml:"name"`
	In       string         `yaml:"in"`
	Required bool           `yaml:"required,omitempty"`
	Schema   map[string]any `yaml:"schema,omitempty"`
}

// MediaType holds the JSON Schema for one content type of a RequestBody.
// Only the "application/json" entry is read by the gateway (see
// internal/gateway's validateBody).
type MediaType struct {
	Schema map[string]any `yaml:"schema,omitempty"`
}

// RequestBody is an OpenAPI requestBody declaration: Required governs
// whether a missing body is rejected, and each Content entry's Schema is
// validated against the decoded body (see internal/validate.Schema).
type RequestBody struct {
	Required bool                 `yaml:"required,omitempty"`
	Content  map[string]MediaType `yaml:"content,omitempty"`
}

// Operation is one HTTP method entry under a path (OpenAPI's operation
// object), extended with the "x-temporal" list describing what it does
// against Temporal.
type Operation struct {
	OperationID string            `yaml:"operationId"`
	Summary     string            `yaml:"summary,omitempty"`
	Parameters  []Parameter       `yaml:"parameters,omitempty"`
	RequestBody *RequestBody      `yaml:"requestBody,omitempty"`
	Responses   map[string]any    `yaml:"responses,omitempty"`
	Temporal    []TemporalBinding `yaml:"x-temporal"`
}

// PathItem is the set of HTTP methods declared for one path (OpenAPI's path
// item object). Only the methods the gateway actually routes are modeled;
// OPTIONS/HEAD/TRACE are not supported.
type PathItem struct {
	Get    *Operation `yaml:"get,omitempty"`
	Post   *Operation `yaml:"post,omitempty"`
	Put    *Operation `yaml:"put,omitempty"`
	Patch  *Operation `yaml:"patch,omitempty"`
	Delete *Operation `yaml:"delete,omitempty"`
}

// Info is the OpenAPI document's info object.
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

// Load reads path, expands "${VAR}"/"${VAR:-default}" environment
// references, parses it as the Spec YAML document, and validates every
// operation's x-temporal bindings (see validate.go) before returning it -
// so a misconfigured spec fails at startup rather than on the first
// request that hits the bad route.
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
