package temporal

import (
	"fmt"

	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"

	"temporal-gateway/internal/spec"
)

// NexusWorkers is the set of embedded Temporal workers the "nexus" driver
// needs at runtime: one per (namespace, taskQueue) hosting a CascadeEvent
// workflow, and one per namespace targeted by a nexus-driver trigger,
// hosting the Dispatch Nexus service that lets a CascadeEvent workflow
// running elsewhere reach it (see CascadeEvent and NewDispatchService).
// Built once at startup from whichever operations select x-temporal.driver:
// nexus (see BuildNexusWorkers) - empty, not nil, when none do, so a
// gateway that only uses the direct driver never polls a Nexus task queue
// at all.
type NexusWorkers []worker.Worker

// BuildNexusWorkers inspects apiSpec for operations using the "nexus"
// driver and constructs the workers described by NexusWorkers, wiring the
// Dispatch Nexus service to dispatcher - the same one the direct driver
// uses (see internal/gateway.DirectDriver) - so both drivers execute
// triggers through identical Temporal-client logic. A namespace whose
// connection sets NexusDispatchExternal is skipped entirely: some other
// service owns hosting (and validating) that namespace's Dispatch
// operation instead - see examples/order-service for a worked example.
// Returns an empty, unstarted NexusWorkers (see NexusWorkers.Start), not an
// error, if no operation selects the nexus driver. Assumes apiSpec has
// already passed spec.Spec.validate and ValidateNexusConfig, so every
// nexus-driver operation's Config is non-nil and every trigger's namespace
// resolves in connections.
func BuildNexusWorkers(apiSpec *spec.Spec, connections Connections, dispatcher *Dispatcher) (NexusWorkers, error) {
	type cascadeTarget struct {
		namespace, taskQueue string
	}
	cascadeWorkflowNames := map[cascadeTarget]map[string]bool{}
	dispatchNamespaces := map[string]bool{}

	for _, route := range apiSpec.Routes() {
		t := route.Operation.Temporal
		if t.DriverOrDefault() != spec.DriverNexus || t.Config == nil {
			continue
		}

		target := cascadeTarget{namespace: t.Config.Namespace, taskQueue: t.Config.TaskQueue}
		if cascadeWorkflowNames[target] == nil {
			cascadeWorkflowNames[target] = map[string]bool{}
		}
		cascadeWorkflowNames[target][t.Config.WorkflowTypeOrDefault()] = true

		for _, trigger := range t.Triggers {
			dispatchNamespaces[trigger.Namespace] = true
		}
	}

	var workers NexusWorkers

	for target, names := range cascadeWorkflowNames {
		conn, err := connections.resolve(target.namespace)
		if err != nil {
			return nil, fmt.Errorf("nexus driver: cascade workflow: %w", err)
		}
		w := worker.New(conn.Client, target.taskQueue, worker.Options{})
		for name := range names {
			w.RegisterWorkflowWithOptions(CascadeEvent, workflow.RegisterOptions{Name: name})
		}
		workers = append(workers, w)
	}

	for namespace := range dispatchNamespaces {
		conn, err := connections.resolve(namespace)
		if err != nil {
			return nil, fmt.Errorf("nexus driver: dispatch service: %w", err)
		}
		if conn.DispatchExternal {
			continue
		}
		w := worker.New(conn.Client, conn.DispatchTaskQueue, worker.Options{})
		w.RegisterNexusService(NewDispatchService(dispatcher))
		workers = append(workers, w)
	}

	return workers, nil
}

// Start starts every worker in ws. If any fails to start, the ones already
// started are left running for the caller to Stop (e.g. via a deferred
// NexusWorkers.Stop covering the whole process lifetime) - Start doesn't
// unwind partial startup itself, since main always stops every worker it
// built on shutdown regardless of how far startup got.
func (ws NexusWorkers) Start() error {
	for _, w := range ws {
		if err := w.Start(); err != nil {
			return fmt.Errorf("nexus driver: start worker: %w", err)
		}
	}
	return nil
}

// Stop stops every worker in ws.
func (ws NexusWorkers) Stop() {
	for _, w := range ws {
		w.Stop()
	}
}
