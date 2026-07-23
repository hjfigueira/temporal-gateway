package temporal

import (
	"context"
	"fmt"

	"go.temporal.io/sdk/client"

	"temporal-gateway/internal/response"
	"temporal-gateway/internal/spec"
)

// Dispatcher executes the Temporal action described by a route's
// x-temporal binding against a real Temporal client.
type Dispatcher struct {
	client  client.Client
	catalog *Catalog
}

func NewDispatcher(c client.Client, catalog *Catalog) *Dispatcher {
	return &Dispatcher{client: c, catalog: catalog}
}

// Dispatch runs binding's action against workflowID, using body (may be
// nil) as the workflow/signal/query input where applicable. It returns a
// JSON-serializable result.
func (d *Dispatcher) Dispatch(ctx context.Context, binding spec.TemporalBinding, workflowID string, body any) (any, error) {
	switch binding.Action {
	case spec.ActionStartWorkflow:
		return d.startWorkflow(ctx, binding, workflowID, body)
	case spec.ActionSignalWorkflow:
		if err := d.client.SignalWorkflow(ctx, workflowID, "", binding.SignalName, body); err != nil {
			return nil, err
		}
		return response.WorkflowSignaled{
			Envelope:   response.Envelope{Status: response.StatusSignaled},
			WorkflowID: workflowID,
			SignalName: binding.SignalName,
		}, nil
	case spec.ActionQueryWorkflow:
		return d.queryWorkflow(ctx, binding, workflowID, body)
	case spec.ActionCancelWorkflow:
		if err := d.client.CancelWorkflow(ctx, workflowID, ""); err != nil {
			return nil, err
		}
		return response.WorkflowAck{
			Envelope:   response.Envelope{Status: response.StatusCancelled},
			WorkflowID: workflowID,
		}, nil
	case spec.ActionTerminateWorkflow:
		reason, _ := body.(string)
		if m, ok := body.(map[string]any); ok {
			if r, ok := m["reason"].(string); ok {
				reason = r
			}
		}
		if err := d.client.TerminateWorkflow(ctx, workflowID, "", reason); err != nil {
			return nil, err
		}
		return response.WorkflowAck{
			Envelope:   response.Envelope{Status: response.StatusTerminated},
			WorkflowID: workflowID,
		}, nil
	case spec.ActionGetResult:
		return d.getResult(ctx, workflowID)
	default:
		return nil, fmt.Errorf("unsupported temporal action %q", binding.Action)
	}
}

// queryWorkflow issues a Temporal query and returns its decoded result
// as-is: unlike the other actions, a query's response is caller-defined
// business data, not a gateway operation-status acknowledgement, so it's
// not wrapped in a response.Envelope.
func (d *Dispatcher) queryWorkflow(ctx context.Context, binding spec.TemporalBinding, workflowID string, body any) (any, error) {
	var args []interface{}
	if body != nil {
		args = append(args, body)
	}

	value, err := d.client.QueryWorkflow(ctx, workflowID, "", binding.QueryType, args...)
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
func (d *Dispatcher) getResult(ctx context.Context, workflowID string) (any, error) {
	run := d.client.GetWorkflow(ctx, workflowID, "")

	var result any
	if err := run.Get(ctx, &result); err != nil {
		return nil, err
	}
	return result, nil
}
