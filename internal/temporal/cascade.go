package temporal

import (
	"time"

	"go.temporal.io/sdk/workflow"

	"temporal-gateway/internal/spec"
)

// DispatchServiceName is the Nexus service every namespace's Dispatch
// worker exposes - see NewDispatchService.
const DispatchServiceName = "TemporalGatewayDispatch"

// DispatchOperationName is DispatchServiceName's single operation.
const DispatchOperationName = "Dispatch"

// CascadeFilterActivityName is the Activity CascadeEvent calls, once per
// trigger, when spec.CascadeInput.FilterTaskQueue is set - see CascadeEvent.
// Unlike DispatchServiceName (a Nexus service, hosted by whichever service
// owns the target namespace's workflow), this is a plain Temporal Activity:
// the simplest way to plug a relevance decision into an architectural
// workflow the gateway itself runs, with no Nexus protocol involved. Any
// worker polling FilterTaskQueue in CascadeEvent's own namespace -
// Activities can't cross namespaces, unlike Nexus operations - can serve
// it, in any SDK; see notification-service/rrbuild/cascadefilter/plugin.go
// for a worked example: a custom RoadRunner plugin that speaks the
// Temporal Go SDK itself but forwards every task into its own PHP pool
// (src/CascadeFilterActivity.php) for the actual business rule, so "which
// language answers this" and "where the business logic lives" stay
// separate concerns.
const CascadeFilterActivityName = "IsEventRelevant"

// CascadeFilterInput is CascadeFilterActivityName's input.
type CascadeFilterInput struct {
	Namespace string `json:"namespace"`
	Body      any    `json:"body,omitempty"`
}

// CascadeFilterOutput is CascadeFilterActivityName's result: Reason is
// empty when the event is relevant, or explains why it isn't - the same
// null-or-reason shape notification-service's own isEventRelevant Query
// uses (see BusinessRulesWorkflow), just carried over an Activity instead.
type CascadeFilterOutput struct {
	Reason string `json:"reason,omitempty"`
}

// cascadeFilterActivityOptions bounds how long CascadeEvent waits on a
// single filter check - generous enough for a real business-rule lookup,
// short enough that one slow/stuck filter worker doesn't stall a whole
// trigger indefinitely.
var cascadeFilterActivityOptions = workflow.ActivityOptions{StartToCloseTimeout: 10 * time.Second}

// CascadeEvent is the workflow the "nexus" driver starts in place of
// dispatching an operation's x-temporal.triggers directly (see
// internal/gateway.NexusDriver): given a spec.CascadeInput, it optionally
// filters each trigger first (see below), then fans the surviving ones out
// concurrently to the Nexus endpoint reaching its target namespace
// (spec.CascadeTrigger.NexusEndpoint), invoking DispatchOperationName there
// - exposed by that namespace's own Dispatch worker, see NewDispatchService
// - and waits for all of them to finish before returning their combined
// outcome.
//
// When input.FilterTaskQueue is set, CascadeEvent calls
// CascadeFilterActivityName once per trigger, synchronously, before that
// trigger's Nexus dispatch: a rejected trigger's Nexus operation is never
// invoked at all (recorded as this trigger's own error, same as a Nexus
// rejection would be - see spec.CascadeTriggerResult), so no workflow run
// is created for it by this path either. Left unset, every trigger is
// dispatched unconditionally, same as before this existed.
//
// This workflow must be registered - by exactly the name each operation's
// x-temporal.config.workflowType declares (spec.NexusConfig.
// WorkflowTypeOrDefault, "CascadeEvent" if unset) - on a worker polling
// that config's namespace/taskQueue; see BuildNexusWorkers, which does this
// for every nexus-driver operation in the loaded spec.
func CascadeEvent(ctx workflow.Context, input spec.CascadeInput) (spec.CascadeResult, error) {
	result := spec.CascadeResult{Results: make([]spec.CascadeTriggerResult, len(input.Triggers))}

	filterCtx := workflow.WithActivityOptions(ctx, cascadeFilterActivityOptions)

	selector := workflow.NewSelector(ctx)
	pending := 0
	for i, trigger := range input.Triggers {
		i, trigger := i, trigger

		result.Results[i] = spec.CascadeTriggerResult{
			Namespace:  trigger.Namespace,
			WorkflowID: trigger.WorkflowID,
			Action:     trigger.Action,
		}

		if input.FilterTaskQueue != "" {
			var filterOutput CascadeFilterOutput
			err := workflow.ExecuteActivity(
				workflow.WithTaskQueue(filterCtx, input.FilterTaskQueue),
				CascadeFilterActivityName,
				CascadeFilterInput{Namespace: trigger.Namespace, Body: input.Body},
			).Get(ctx, &filterOutput)

			if err != nil {
				result.Results[i].Error = err.Error()
				continue
			}
			if filterOutput.Reason != "" {
				result.Results[i].Error = filterOutput.Reason
				continue
			}
		}

		pending++
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

	for range pending {
		selector.Select(ctx)
	}

	return result, nil
}
