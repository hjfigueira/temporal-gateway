package temporal

import (
	"go.temporal.io/sdk/workflow"

	"temporal-gateway/internal/spec"
)

// DispatchServiceName is the Nexus service every namespace's Dispatch
// worker exposes - see NewDispatchService.
const DispatchServiceName = "TemporalGatewayDispatch"

// DispatchOperationName is DispatchServiceName's single operation.
const DispatchOperationName = "Dispatch"

// CascadeEvent is the workflow the "nexus" driver starts in place of
// dispatching an operation's x-temporal.triggers directly (see
// internal/gateway.NexusDriver): given a spec.CascadeInput, it fans each
// trigger out concurrently to the Nexus endpoint reaching its target
// namespace (spec.CascadeTrigger.NexusEndpoint), invoking
// DispatchOperationName there - exposed by that namespace's own Dispatch
// worker, see NewDispatchService - and waits for all of them to finish
// before returning their combined outcome.
//
// This workflow must be registered - by exactly the name each operation's
// x-temporal.config.workflowType declares (spec.NexusConfig.
// WorkflowTypeOrDefault, "CascadeEvent" if unset) - on a worker polling
// that config's namespace/taskQueue; see BuildNexusWorkers, which does this
// for every nexus-driver operation in the loaded spec.
func CascadeEvent(ctx workflow.Context, input spec.CascadeInput) (spec.CascadeResult, error) {
	result := spec.CascadeResult{Results: make([]spec.CascadeTriggerResult, len(input.Triggers))}

	selector := workflow.NewSelector(ctx)
	for i, trigger := range input.Triggers {
		i, trigger := i, trigger

		result.Results[i] = spec.CascadeTriggerResult{
			Namespace:  trigger.Namespace,
			WorkflowID: trigger.WorkflowID,
			Action:     trigger.Action,
		}

		client := workflow.NewNexusClient(trigger.NexusEndpoint, DispatchServiceName)
		future := client.ExecuteOperation(ctx, DispatchOperationName, spec.DispatchInput{
			Binding: trigger.TemporalBinding,
			Body:    input.Body,
		}, workflow.NexusOperationOptions{})

		selector.AddFuture(future, func(f workflow.Future) {
			var output spec.DispatchOutput
			if err := f.Get(ctx, &output); err != nil {
				result.Results[i].Error = err.Error()
				return
			}
			result.Results[i].Result = output.Result
		})
	}

	for range input.Triggers {
		selector.Select(ctx)
	}

	return result, nil
}
