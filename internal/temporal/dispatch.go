package temporal

import (
	"context"
	"fmt"

	"temporal-gateway/internal/response"
	"temporal-gateway/internal/spec"
)

// Dispatcher executes the Temporal action described by a route's
// x-temporal binding against the Connections dictionary entry its
// Namespace names.
type Dispatcher struct {
	connections Connections
}

func NewDispatcher(connections Connections) *Dispatcher {
	return &Dispatcher{connections: connections}
}

// action runs one Temporal action against an already-resolved connection.
// body may be nil; it's the workflow/signal/query input where applicable.
type action func(ctx context.Context, conn *Connection, binding spec.TemporalBinding, workflowID string, body any) (response.Outcome, error)

// actions implements every spec.TemporalAction. A new action is an entry
// here plus its validation rule in spec (TestEverySpecActionIsImplemented
// keeps the two in step).
var actions = map[spec.TemporalAction]action{
	spec.ActionStartWorkflow:     startWorkflow,
	spec.ActionSignalWorkflow:    signalWorkflow,
	spec.ActionQueryWorkflow:     queryWorkflow,
	spec.ActionCancelWorkflow:    cancelWorkflow,
	spec.ActionTerminateWorkflow: terminateWorkflow,
	spec.ActionGetResult:         getResult,
}

// Dispatch resolves binding's namespace to a connection, then runs the
// action implementing binding.Action with workflowID and body.
func (d *Dispatcher) Dispatch(ctx context.Context, binding spec.TemporalBinding, workflowID string, body any) (response.Outcome, error) {
	run, ok := actions[binding.Action]
	if !ok {
		return response.Outcome{}, fmt.Errorf("unsupported temporal action %q", binding.Action)
	}
	conn, err := d.connections.resolve(binding.Namespace)
	if err != nil {
		return response.Outcome{}, err
	}
	return run(ctx, conn, binding, workflowID, body)
}

// signalWorkflow sends binding.SignalName to workflowID, with body as the
// signal's input.
func signalWorkflow(ctx context.Context, conn *Connection, binding spec.TemporalBinding, workflowID string, body any) (response.Outcome, error) {
	if err := conn.Client().SignalWorkflow(ctx, workflowID, "", binding.SignalName, body); err != nil {
		return response.Outcome{}, err
	}
	signaled := response.WorkflowSignaled{
		Envelope:   response.Envelope{Status: response.StatusSignaled},
		WorkflowID: workflowID,
		SignalName: binding.SignalName,
	}
	return response.Ack(signaled, signaled.Envelope), nil
}

// cancelWorkflow requests cancellation of workflowID's current run.
func cancelWorkflow(ctx context.Context, conn *Connection, _ spec.TemporalBinding, workflowID string, _ any) (response.Outcome, error) {
	if err := conn.Client().CancelWorkflow(ctx, workflowID, ""); err != nil {
		return response.Outcome{}, err
	}
	ack := response.WorkflowAck{
		Envelope:   response.Envelope{Status: response.StatusCancelled},
		WorkflowID: workflowID,
	}
	return response.Ack(ack, ack.Envelope), nil
}

// terminateWorkflow immediately terminates workflowID's current run. The
// termination reason recorded against the run comes from body's "reason"
// field, or from body itself when it's a plain string.
func terminateWorkflow(ctx context.Context, conn *Connection, _ spec.TemporalBinding, workflowID string, body any) (response.Outcome, error) {
	reason, _ := body.(string)
	if m, ok := body.(map[string]any); ok {
		if r, ok := m["reason"].(string); ok {
			reason = r
		}
	}
	if err := conn.Client().TerminateWorkflow(ctx, workflowID, "", reason); err != nil {
		return response.Outcome{}, err
	}
	ack := response.WorkflowAck{
		Envelope:   response.Envelope{Status: response.StatusTerminated},
		WorkflowID: workflowID,
	}
	return response.Ack(ack, ack.Envelope), nil
}

// queryWorkflow issues a Temporal query and returns its decoded result
// as-is (response.Raw): unlike the other actions, a query's response is
// caller-defined business data, not a gateway operation-status
// acknowledgement, so it's not wrapped in a response.Envelope.
func queryWorkflow(ctx context.Context, conn *Connection, binding spec.TemporalBinding, workflowID string, body any) (response.Outcome, error) {
	var args []any
	if body != nil {
		args = append(args, body)
	}

	value, err := conn.Client().QueryWorkflow(ctx, workflowID, "", binding.QueryType, args...)
	if err != nil {
		return response.Outcome{}, err
	}

	var result any
	if err := value.Get(&result); err != nil {
		return response.Outcome{}, err
	}
	return response.Raw(result), nil
}

// getResult blocks until workflowID's current run completes and returns its
// decoded result as-is, for the same reason queryWorkflow does.
func getResult(ctx context.Context, conn *Connection, _ spec.TemporalBinding, workflowID string, _ any) (response.Outcome, error) {
	run := conn.Client().GetWorkflow(ctx, workflowID, "")

	var result any
	if err := run.Get(ctx, &result); err != nil {
		return response.Outcome{}, err
	}
	return response.Raw(result), nil
}
