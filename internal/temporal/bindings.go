package temporal

import (
	"errors"
	"fmt"

	"temporal-gateway/internal/spec"
)

// ValidateBindings checks every x-temporal binding in apiSpec against
// conns: its namespace must have a temporal.connections entry, and a
// startWorkflow binding without its own taskQueue must find one in that
// namespace's workflow catalog. Every problem is joined (rather than
// stopping at the first) so every bad reference is reported in one pass.
// Called once at startup, so a spec that doesn't match config.yml fails
// before the server starts serving requests rather than as a per-request
// dispatch error the first time that route is hit.
func ValidateBindings(apiSpec *spec.Spec, conns Connections) error {
	var errs []error
	for _, route := range apiSpec.Routes() {
		for i, binding := range route.Operation.Temporal.Triggers {
			where := fmt.Sprintf("%s %s: x-temporal.triggers[%d]", route.Method, route.Path, i)
			conn, ok := conns[binding.Namespace]
			if !ok {
				errs = append(errs, fmt.Errorf("%s: namespace %q has no temporal.connections entry in config", where, binding.Namespace))
				continue
			}
			if binding.Action != spec.ActionStartWorkflow || binding.TaskQueue != "" {
				continue
			}
			if _, ok := conn.Catalog.TaskQueueFor(binding.WorkflowType); !ok {
				errs = append(errs, fmt.Errorf("%s: startWorkflow has no taskQueue, and namespace %q's workflows in config declare none for %q", where, binding.Namespace, binding.WorkflowType))
			}
		}
	}
	return errors.Join(errs...)
}
