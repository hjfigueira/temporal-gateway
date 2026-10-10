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

	"github.com/getkin/kin-openapi/openapi3"
	"gopkg.in/yaml.v3"

	"temporal-gateway/internal/config/envsubst"
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

// TemporalSpec is the "x-temporal" vendor extension attached to an
// operation: the list of Temporal actions it dispatches to (Triggers) plus
// how their outcomes combine into the operation's overall HTTP response
// (ReturnStrategy).
type TemporalSpec struct {
	// ReturnStrategy defaults to ReturnStrategyAcceptPartial when empty (see
	// Strategy).
	ReturnStrategy ReturnStrategy    `yaml:"returnStrategy,omitempty"`
	Triggers       []TemporalBinding `yaml:"triggers"`
}

// Strategy returns t.ReturnStrategy, defaulting to ReturnStrategyAcceptPartial
// when it wasn't set in the spec.
func (t TemporalSpec) Strategy() ReturnStrategy {
	if t.ReturnStrategy == "" {
		return ReturnStrategyAcceptPartial
	}
	return t.ReturnStrategy
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

// Operation is the gateway's view of one HTTP method entry under a path
// (OpenAPI's operation object): its id and the "x-temporal" extension
// describing what it does against Temporal. Parameters, request bodies and
// everything else OpenAPI describes are read by kin-openapi instead (see
// Spec.doc and ADR-029).
type Operation struct {
	OperationID string       `yaml:"operationId"`
	Summary     string       `yaml:"summary,omitempty"`
	Temporal    TemporalSpec `yaml:"x-temporal"`
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

	// doc is the same merged document as parsed by kin-openapi, which
	// validates requests against it (see Route.OpenAPI). Nil for a Spec
	// built in code rather than by Load.
	doc *openapi3.T
}

// Load reads each of paths, expands "${VAR}"/"${VAR:-default}" environment
// references in each, and merges them in order into one document (see
// mergeDoc) - a later file can extend or override an earlier one (e.g. a
// base spec plus an environment-specific overrides file), and may $ref the
// earlier file's components. That one document is then decoded twice: into
// Spec for operation ids and x-temporal (as YAML, so integers stay exact),
// and by kin-openapi for everything OpenAPI describes. The result is
// validated (see validate.go) before being returned, so a misconfigured spec
// fails at startup rather than on the first request that hits the bad route.
func Load(paths ...string) (*Spec, error) {
	if len(paths) == 0 {
		return nil, fmt.Errorf("spec: no api spec paths given")
	}

	merged := map[string]any{}
	for _, path := range paths {
		doc, err := loadOne(path)
		if err != nil {
			return nil, err
		}
		mergeDoc(merged, doc)
	}

	data, err := yaml.Marshal(merged)
	if err != nil {
		return nil, fmt.Errorf("spec: %w", err)
	}
	var s Spec
	if err := yaml.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("spec: parse %v: %w", paths, err)
	}
	// $refs to other files aren't supported: the merged document has no
	// single location to resolve them from, and the loader refuses them.
	if s.doc, err = openapi3.NewLoader().LoadFromData(data); err != nil {
		return nil, fmt.Errorf("spec: parse %v: %w", paths, err)
	}

	if err := s.validate(); err != nil {
		return nil, fmt.Errorf("spec: %w", err)
	}
	return &s, nil
}

// loadOne reads, expands and parses a single spec file - see Load, which
// merges the result across every configured path.
func loadOne(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("spec: read %q: %w", path, err)
	}

	data, err = envsubst.Expand(data)
	if err != nil {
		return nil, fmt.Errorf("spec: %q: %w", path, err)
	}

	var doc map[string]any
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("spec: parse %q: %w", path, err)
	}
	return doc, nil
}

// mergeDoc folds a later spec file's document into merged (ADR-012). Each
// operation (paths.<path>.<method>), path-level field, component
// (components.<section>.<name>) and info field the later file declares
// replaces the earlier one whole, rather than the two being combined
// field by field; any other top-level key is replaced outright.
func mergeDoc(merged, doc map[string]any) {
	for key, value := range doc {
		switch key {
		case "paths", "components":
			mergeLevels(merged, key, value, 2)
		case "info":
			mergeLevels(merged, key, value, 1)
		default:
			merged[key] = value
		}
	}
}

// mergeLevels sets dst[key] = value, except that while depth > 0 and both
// sides are mappings it merges them key by key, one level deeper each time.
func mergeLevels(dst map[string]any, key string, value any, depth int) {
	src, srcOK := value.(map[string]any)
	existing, dstOK := dst[key].(map[string]any)
	if depth == 0 || !srcOK || !dstOK {
		dst[key] = value
		return
	}
	for k, v := range src {
		mergeLevels(existing, k, v, depth-1)
	}
}
