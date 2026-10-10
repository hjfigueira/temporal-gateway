// Package response defines the shared JSON envelope every HTTP response the
// gateway writes is built on: a machine-readable status code and a
// human-readable summary. Specific response shapes embed Envelope and add
// their own fields via composition, so every response the gateway writes -
// success or failure - shares the same status/message contract.
package response

// Status is a machine-readable outcome code, playing the same role an HTTP
// status code does but at the level of a single Temporal action - a batch
// request can carry several of these at once (see HTTP 207 Multi-Status),
// one per binding, which a single overall HTTP status code can't express.
type Status string

const (
	// StatusValid Successful outcomes, one per Temporal action. StatusStarted means
	// ExecuteWorkflow actually created a new run - never assume this just
	// because the call didn't error: WorkflowIDConflictPolicy/
	// WorkflowIDReusePolicy can make it succeed by silently attaching to an
	// existing run instead (see the StatusWorkflow* outcomes below).
	StatusValid      Status = "VALID"
	StatusStarted    Status = "STARTED"
	StatusSignaled   Status = "SIGNALED"
	StatusCancelled  Status = "CANCELLED"
	StatusTerminated Status = "TERMINATED"

	// StatusWorkflowStarted Top-level outcomes for a route that dispatches more than one
	// x-temporal binding in a single call (see BatchResult): every binding
	// started, at least one (but not all) did, or none did.
	StatusWorkflowStarted          Status = "WORKFLOW_STARTED"
	StatusWorkflowPartiallyStarted Status = "WORKFLOW_PARTIALLY_STARTED"
	StatusWorkflowNotStarted       Status = "WORKFLOW_NOT_STARTED"

	// StatusWorkflowRunning Outcomes for a startWorkflow binding that succeeded without creating
	// a new run: the workflow ID was already in use, and the configured
	// WorkflowIDConflictPolicy/WorkflowIDReusePolicy allowed the call to
	// return the existing execution instead of erroring. These describe
	// that execution's actual state rather than claiming STARTED.
	StatusWorkflowRunning    Status = "WORKFLOW_RUNNING"
	StatusWorkflowCompleted  Status = "WORKFLOW_COMPLETED"
	StatusWorkflowFailed     Status = "WORKFLOW_FAILED"
	StatusWorkflowCancelled  Status = "WORKFLOW_CANCELLED"
	StatusWorkflowTerminated Status = "WORKFLOW_TERMINATED"
	StatusWorkflowTimedOut   Status = "WORKFLOW_TIMED_OUT"

	// StatusDuplicated Failure outcomes.
	StatusDuplicated       Status = "DUPLICATED" // WorkflowExecutionAlreadyStarted
	StatusNotFound         Status = "NOT_FOUND"
	StatusInvalidArgument  Status = "INVALID_ARGUMENT" // rejected by the Temporal server
	StatusForbidden        Status = "FORBIDDEN"
	StatusInvalidRequest   Status = "INVALID_REQUEST"   // malformed/missing HTTP request body
	StatusValidationFailed Status = "VALIDATION_FAILED" // body doesn't match the declared schema
	StatusPayloadTooLarge  Status = "PAYLOAD_TOO_LARGE" // body exceeds server.maxBodyBytes
	StatusTimeout          Status = "TIMEOUT"           // dispatch exceeded server.requestTimeout
	StatusFailed           Status = "FAILED"            // fallback for any other dispatch error
)

// IsError reports whether status represents a failed outcome or a success
// that didn't do what was asked (e.g., a startWorkflow binding that
// attached to an already-existing run instead of creating a new one).
func (s Status) IsError() bool {
	switch s {
	case StatusWorkflowPartiallyStarted, StatusWorkflowNotStarted,
		StatusWorkflowRunning, StatusWorkflowCompleted, StatusWorkflowFailed,
		StatusWorkflowCancelled, StatusWorkflowTerminated, StatusWorkflowTimedOut,
		StatusDuplicated, StatusNotFound, StatusInvalidArgument, StatusForbidden,
		StatusInvalidRequest, StatusValidationFailed, StatusPayloadTooLarge,
		StatusTimeout, StatusFailed:
		return true
	default:
		return false
	}
}

// Envelope is the common shape of every JSON response the gateway writes.
type Envelope struct {
	Status  Status `json:"status"`
	Message string `json:"message,omitempty"`
}

// GetStatus returns the envelope's status. Since every response type in
// this package embeds Envelope, this method is promoted onto all of them,
// letting a caller extract the outcome from an opaque result value (e.g.
// dispatchOutcome.result, typed as any) via a single-method interface
// instead of a type switch over every concrete response type.
func (e Envelope) GetStatus() Status {
	return e.Status
}

// WorkflowStarted is returned when a startWorkflow binding successfully
// dispatches: the envelope plus the identifiers needed to address the new
// run (query it, signal it, wait on its result, ...).
type WorkflowStarted struct {
	Envelope
	WorkflowID string `json:"workflowId"`
	RunID      string `json:"runId"`
}

// WorkflowSignaled is returned when a signalWorkflow binding successfully
// dispatches.
type WorkflowSignaled struct {
	Envelope
	WorkflowID string `json:"workflowId"`
	SignalName string `json:"signalName"`
}

// WorkflowAck is returned when a cancelWorkflow or terminateWorkflow
// binding successfully dispatches - both are fire-and-forget requests to
// the server with no result beyond acknowledgement.
type WorkflowAck struct {
	Envelope
	WorkflowID string `json:"workflowId"`
}

// BatchResult is the top-level response for a route that dispatches more
// than one x-temporal binding in a single call: the overall envelope -
// StatusWorkflowStarted if every binding succeeded, StatusWorkflowNotStarted
// if none did, or StatusWorkflowPartiallyStarted if it's a genuine mix of
// both - plus each binding's own per-item result (a
// WorkflowStarted/WorkflowSignaled/WorkflowAck on success, or an Envelope
// naming the failure).
type BatchResult struct {
	Envelope
	Results []any `json:"results"`
}
