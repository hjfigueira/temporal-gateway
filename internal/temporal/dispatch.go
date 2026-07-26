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

// Dispatch resolves binding's namespace to a connection, then routes to the
// method implementing binding.Action, passing along workflowID and body
// (may be nil, used as the workflow/signal/query input where applicable).
// It returns a JSON-serializable result.
func (d *Dispatcher) Dispatch(ctx context.Context, binding spec.TemporalBinding, workflowID string, body any) (any, error) {
	conn, err := d.connections.resolve(binding.Namespace)
	if err != nil {
		return nil, err
	}

	switch binding.Action {
	case spec.ActionStartWorkflow:
		return d.startWorkflow(ctx, conn, binding, workflowID, body)
	case spec.ActionSignalWorkflow:
		return d.signalWorkflow(ctx, conn, binding, workflowID, body)
	case spec.ActionQueryWorkflow:
		return d.queryWorkflow(ctx, conn, binding, workflowID, body)
	case spec.ActionCancelWorkflow:
		return d.cancelWorkflow(ctx, conn, workflowID)
	case spec.ActionTerminateWorkflow:
		return d.terminateWorkflow(ctx, conn, workflowID, body)
	case spec.ActionGetResult:
		return d.getResult(ctx, conn, workflowID)
	default:
		return nil, fmt.Errorf("unsupported temporal action %q", binding.Action)
	}
}

// signalWorkflow sends binding.SignalName to workflowID, with body as the
// signal's input.
func (d *Dispatcher) signalWorkflow(ctx context.Context, conn *Connection, binding spec.TemporalBinding, workflowID string, body any) (any, error) {
	if err := conn.Client.SignalWorkflow(ctx, workflowID, "", binding.SignalName, body); err != nil {
		return nil, err
	}
	return response.WorkflowSignaled{
		Envelope:   response.Envelope{Status: response.StatusSignaled},
		WorkflowID: workflowID,
		SignalName: binding.SignalName,
	}, nil
}

// cancelWorkflow requests cancellation of workflowID's current run.
func (d *Dispatcher) cancelWorkflow(ctx context.Context, conn *Connection, workflowID string) (any, error) {
	if err := conn.Client.CancelWorkflow(ctx, workflowID, ""); err != nil {
		return nil, err
	}
	return response.WorkflowAck{
		Envelope:   response.Envelope{Status: response.StatusCancelled},
		WorkflowID: workflowID,
	}, nil
}

// terminateWorkflow immediately terminates workflowID's current run. The
// termination reason recorded against the run comes from body's "reason"
// field, or from body itself when it's a plain string.
func (d *Dispatcher) terminateWorkflow(ctx context.Context, conn *Connection, workflowID string, body any) (any, error) {
	reason, _ := body.(string)
	if m, ok := body.(map[string]any); ok {
		if r, ok := m["reason"].(string); ok {
			reason = r
		}
	}
	if err := conn.Client.TerminateWorkflow(ctx, workflowID, "", reason); err != nil {
		return nil, err
	}
	return response.WorkflowAck{
		Envelope:   response.Envelope{Status: response.StatusTerminated},
		WorkflowID: workflowID,
	}, nil
}

// queryWorkflow issues a Temporal query and returns its decoded result
// as-is: unlike the other actions, a query's response is caller-defined
// business data, not a gateway operation-status acknowledgement, so it's
// not wrapped in a response.Envelope.
func (d *Dispatcher) queryWorkflow(ctx context.Context, conn *Connection, binding spec.TemporalBinding, workflowID string, body any) (any, error) {
	var args []interface{}
	if body != nil {
		args = append(args, body)
	}

	value, err := conn.Client.QueryWorkflow(ctx, workflowID, "", binding.QueryType, args...)
	if err != nil {
		return nil, err
	}

	var result any
	if err := value.Get(&result); err != nil {
		return nil, err
	}
	return result, nil
}

// getResult blocks until workflowID's current run completes and returns its
// decoded result as-is, for the same reason queryWorkflow does.
func (d *Dispatcher) getResult(ctx context.Context, conn *Connection, workflowID string) (any, error) {
	run := conn.Client.GetWorkflow(ctx, workflowID, "")

	var result any
	if err := run.Get(ctx, &result); err != nil {
		return nil, err
	}
	return result, nil
}
