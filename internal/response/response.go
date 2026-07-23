// Package response defines the shared JSON envelope every HTTP response the
// gateway writes is built on: whether the operation succeeded and a
// human-readable summary. Specific response shapes embed Envelope and add
// their own fields via composition, so every response the gateway writes -
// success or failure - shares the same success/message contract.
package response

// Envelope is the common shape of every JSON response the gateway writes.
type Envelope struct {
	Success bool   `json:"success"`
	Message string `json:"message,omitempty"`
}

// WorkflowStarted is returned when a startWorkflow binding successfully
// dispatches: the envelope plus the identifiers needed to address the new
// run (query it, signal it, wait on its result, ...).
type WorkflowStarted struct {
	Envelope
	WorkflowID string `json:"workflowId"`
	RunID      string `json:"runId"`
}
