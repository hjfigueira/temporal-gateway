package temporal

import (
	"context"

	"github.com/nexus-rpc/sdk-go/nexus"

	"temporal-gateway/internal/spec"
)

// NewDispatchService builds the Nexus service a target namespace's Dispatch
// worker exposes so a CascadeEvent workflow running elsewhere (see
// CascadeEvent) can start/signal/query/etc a workflow in this namespace
// over Nexus. Its single operation, DispatchOperationName, hands off to
// dispatcher.Dispatch exactly as the "direct" driver does in-process (see
// internal/gateway.DirectDriver) - so both drivers execute a trigger
// through identical logic; only how the call arrives differs.
func NewDispatchService(dispatcher *Dispatcher) *nexus.Service {
	operation := nexus.NewSyncOperation(DispatchOperationName, func(ctx context.Context, input spec.DispatchInput, _ nexus.StartOperationOptions) (spec.DispatchOutput, error) {
		result, err := dispatcher.Dispatch(ctx, input.Binding, input.Binding.WorkflowID, input.Body)
		if err != nil {
			return spec.DispatchOutput{}, err
		}
		return spec.DispatchOutput{Result: result}, nil
	})

	service := nexus.NewService(DispatchServiceName)
	if err := service.Register(operation); err != nil {
		// Unreachable: the single, constant operation registered here
		// always has a name and is never registered twice.
		panic(err)
	}
	return service
}
