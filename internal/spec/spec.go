// Package spec loads the API specification: an OpenAPI 3.0 document whose
// operations carry an "x-temporal" extension naming, under "triggers", one
// or more Temporal actions the HTTP operation maps to (start/signal/query a
// workflow, etc), so a single call can dispatch to multiple workflows - plus
// a sibling "returnStrategy" governing how those triggers' outcomes combine
// into the operation's overall HTTP response when there's more than one
// (see TemporalSpec). The specification may be split across more than one
// file (config.yml's apiSpec), which Load merges into a single Spec, later
// files taking precedence over earlier ones operation-by-operation (see
// Spec.merge). The gateway uses this in-memory representation as the basis
// for generating routes at runtime. Before parsing, each raw file is run
// through envsubst.Expand, so values may reference "${VAR}" or
// "${VAR:-default}" environment variables.
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

// ReturnStrategy governs how an operation's overall HTTP response is derived
// from its triggers' individual outcomes when it has more than one (a
// single-trigger operation always reports that trigger's own outcome
// directly, regardless of this setting).
type ReturnStrategy string

const (
	// ReturnStrategyAcceptPartial is the default: a genuine mix of outcomes
	// (at least one trigger succeeded, at least one didn't) is reported as
	// HTTP 207 Multi-Status, so the caller can see exactly which triggers
	// succeeded. A uniform outcome (all or none) gets an ordinary single
	// status code.
	ReturnStrategyAcceptPartial ReturnStrategy = "acceptPartial"
	// ReturnStrategyAllOrNothing treats anything short of every trigger
	// succeeding as a single failure: the operation reports HTTP 409
	// Conflict whenever at least one trigger didn't complete, rather than
	// 207 for a partial mix.
	ReturnStrategyAllOrNothing ReturnStrategy = "allOrNothing"
)

// Driver selects how an operation's x-temporal.triggers actually get
// dispatched to Temporal - see TemporalSpec.DriverOrDefault.
type Driver string

const (
	// DriverDirect is the default: every trigger is dispatched straight to
	// Temporal, concurrently, exactly as if the caller had used the
	// Temporal SDK itself (see internal/gateway.DirectDriver).
	DriverDirect Driver = "direct"
	// DriverNexus starts a single CascadeEvent workflow (named by Config)
	// instead of dispatching the triggers directly; that workflow fans
	// them out to their target namespaces over Nexus (see
	// internal/temporal.CascadeEvent and internal/gateway.NexusDriver). The
	// gateway's HTTP response then reports only the CascadeEvent
	// workflow's own start outcome, not each trigger's - those happen
	// asynchronously, inside the workflow.
	DriverNexus Driver = "nexus"
)

// DefaultCascadeWorkflowType is the workflow type CascadeEvent is
// registered under when a NexusConfig doesn't set its own WorkflowType.
const DefaultCascadeWorkflowType = "CascadeEvent"

// NexusConfig is the "x-temporal.config" object, meaningful only when
// TemporalSpec.Driver is DriverNexus: the CascadeEvent workflow the gateway
// starts in place of dispatching Triggers directly. Namespace and TaskQueue
// name where that workflow runs - a worker the gateway itself hosts (see
// internal/temporal.BuildNexusWorkers) must be polling that namespace/
// taskQueue and have CascadeEvent registered under WorkflowTypeOrDefault().
type NexusConfig struct {
	Namespace    string `yaml:"namespace"`
	TaskQueue    string `yaml:"taskQueue"`
	WorkflowType string `yaml:"workflowType,omitempty"`
	// WorkflowID may use the same "{origin.field}" templating as a
	// trigger's own WorkflowID (see internal/gateway's renderTemplate).
	WorkflowID string `yaml:"workflowId"`
	// FilterTaskQueue, when set, is a task queue in this same
	// namespace/cluster that CascadeEvent polls once per trigger, via a
	// plain Temporal Activity (internal/temporal.CascadeFilterActivityName)
	// rather than Nexus, to ask whether that trigger's event is even worth
	// dispatching - see internal/temporal.CascadeEvent. Left empty, no
	// filtering happens and every trigger is dispatched unconditionally,
	// same as before this field existed. A worker hosting that activity is
	// not something the gateway builds itself (same as a nexusDispatchExternal
	// namespace's Dispatch worker) - see
	// notification-service/rrbuild/cascadefilter/plugin.go for a worked
	// example.
	FilterTaskQueue string `yaml:"filterTaskQueue,omitempty"`
}

// WorkflowTypeOrDefault returns c.WorkflowType, defaulting to
// DefaultCascadeWorkflowType when it wasn't set in the spec.
func (c NexusConfig) WorkflowTypeOrDefault() string {
	if c.WorkflowType == "" {
		return DefaultCascadeWorkflowType
	}
	return c.WorkflowType
}

// TemporalSpec is the "x-temporal" vendor extension attached to an
// operation: the list of Temporal actions it dispatches to (Triggers), how
// their outcomes combine into the operation's overall HTTP response
// (ReturnStrategy), and which Driver actually carries out that dispatch.
type TemporalSpec struct {
	// ReturnStrategy defaults to ReturnStrategyAcceptPartial when empty (see
	// Strategy).
	ReturnStrategy ReturnStrategy `yaml:"returnStrategy,omitempty"`
	// Driver defaults to DriverDirect when empty (see DriverOrDefault).
	Driver Driver `yaml:"driver,omitempty"`
	// Config is only meaningful (and required) when Driver is DriverNexus -
	// see NexusConfig.
	Config   *NexusConfig      `yaml:"config,omitempty"`
	Triggers []TemporalBinding `yaml:"triggers"`
}

// Strategy returns t.ReturnStrategy, defaulting to ReturnStrategyAcceptPartial
// when it wasn't set in the spec.
func (t TemporalSpec) Strategy() ReturnStrategy {
	if t.ReturnStrategy == "" {
		return ReturnStrategyAcceptPartial
	}
	return t.ReturnStrategy
}

// DriverOrDefault returns t.Driver, defaulting to DriverDirect when it
// wasn't set in the spec.
func (t TemporalSpec) DriverOrDefault() Driver {
	if t.Driver == "" {
		return DriverDirect
	}
	return t.Driver
}

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
// object), extended with the "x-temporal" extension describing what it does
// against Temporal.
type Operation struct {
	OperationID string         `yaml:"operationId"`
	Summary     string         `yaml:"summary,omitempty"`
	Parameters  []Parameter    `yaml:"parameters,omitempty"`
	RequestBody *RequestBody   `yaml:"requestBody,omitempty"`
	Responses   map[string]any `yaml:"responses,omitempty"`
	Temporal    TemporalSpec   `yaml:"x-temporal"`
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

// Spec is the in-memory representation of the loaded API specification,
// possibly merged from more than one file (see Load).
type Spec struct {
	OpenAPI string              `yaml:"openapi"`
	Info    Info                `yaml:"info"`
	Paths   map[string]PathItem `yaml:"paths"`
}

// Load reads each of paths, expands "${VAR}"/"${VAR:-default}" environment
// references in each, parses it as a Spec YAML document, and merges them in
// order into a single Spec (see Spec.merge) - later paths take precedence
// over earlier ones for any operation they both define, so a later file can
// extend or override an earlier one (e.g. a base spec plus an
// environment-specific overrides file). The merged result is validated (see
// validate.go) before being returned, so a misconfigured spec fails at
// startup rather than on the first request that hits the bad route.
func Load(paths ...string) (*Spec, error) {
	if len(paths) == 0 {
		return nil, fmt.Errorf("spec: no api spec paths given")
	}

	merged := &Spec{Paths: map[string]PathItem{}}
	for _, path := range paths {
		s, err := loadOne(path)
		if err != nil {
			return nil, err
		}
		merged.merge(s)
	}

	if err := merged.validate(); err != nil {
		return nil, fmt.Errorf("spec: %w", err)
	}

	return merged, nil
}

// loadOne reads and parses a single spec file - see Load, which merges and
// validates the result across every configured path.
func loadOne(path string) (*Spec, error) {
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

	return &s, nil
}

// merge folds other into s: other's OpenAPI/Info fields override s's where
// set (non-empty), and other's paths are merged in one HTTP method at a
// time - an operation other declares for a (path, method) s already has
// entirely replaces it, rather than the two being combined field by field,
// so a later spec file can cleanly supersede an earlier one's definition of
// the same endpoint.
func (s *Spec) merge(other *Spec) {
	if other.OpenAPI != "" {
		s.OpenAPI = other.OpenAPI
	}
	s.Info.merge(other.Info)

	for path, item := range other.Paths {
		s.Paths[path] = s.Paths[path].merge(item)
	}
}

// merge returns i with every method other sets overriding i's own - see
// Spec.merge.
func (i PathItem) merge(other PathItem) PathItem {
	if other.Get != nil {
		i.Get = other.Get
	}
	if other.Post != nil {
		i.Post = other.Post
	}
	if other.Put != nil {
		i.Put = other.Put
	}
	if other.Patch != nil {
		i.Patch = other.Patch
	}
	if other.Delete != nil {
		i.Delete = other.Delete
	}
	return i
}

// merge overwrites i's fields with any other sets (non-empty) - see
// Spec.merge.
func (i *Info) merge(other Info) {
	if other.Title != "" {
		i.Title = other.Title
	}
	if other.Version != "" {
		i.Version = other.Version
	}
	if other.Description != "" {
		i.Description = other.Description
	}
}
