// Command order-service is a sample "called endpoint" application: it owns
// and runs OrderWorkflow, the workflow the gateway's "nexus" driver starts
// indirectly by cascading through Nexus (see api-spec.yaml's
// createOrderViaCascade operation and README.md's "nexus driver" section).
//
// Unlike every other namespace, which the gateway's own generic Dispatch
// service happily hosts (see internal/temporal.NewDispatchService), the
// "default" namespace's temporal.connections entry in config.yml sets
// nexusDispatchExternal: true - telling the gateway NOT to host a Dispatch
// worker for it. This program is that external worker instead, and the
// whole point of it living here, in the service that actually owns
// OrderWorkflow, rather than inside the generic gateway, is validation:
// CascadeEvent cascades every configured trigger's payload down
// indiscriminately, but not every cascaded event is actually relevant to
// OrderWorkflow. Deciding that is order-service's own business logic, so
// it runs here - see validateOrderPayload - and rejects an irrelevant
// payload before ExecuteWorkflow is ever called, rather than letting a
// generic dispatcher start a workflow run that has nothing useful to do.
//
// It still speaks the exact same Dispatch Nexus contract the gateway's
// generic service does (see internal/temporal.DispatchServiceName/
// DispatchOperationName and spec.DispatchInput/DispatchOutput), so
// CascadeEvent (internal/temporal.CascadeEvent) doesn't need to know or
// care which kind of service is on the other end of a given trigger's
// Nexus endpoint.
//
// Run it alongside the gateway and a Temporal server:
//
//	go run ./cmd/order-service
//
// It reads TEMPORAL_NAMESPACE/TEMPORAL_HOST/ORDERS_TASK_QUEUE the same way
// config.yml and api-spec.yaml do, plus ORDER_SERVICE_NEXUS_TASK_QUEUE for
// its own Dispatch worker's task queue - which is what the "default"
// namespace's Nexus endpoint (gateway-default in config.yml) must actually
// be provisioned to target, e.g.:
//
//	temporal operator nexus endpoint create \
//	  --name gateway-default \
//	  --target-namespace default \
//	  --target-task-queue order-service-nexus-dispatch
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/nexus-rpc/sdk-go/nexus"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporalnexus"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"

	"temporal-gateway/internal/dotenv"
	"temporal-gateway/internal/spec"
	"temporal-gateway/internal/temporal"
)

// orderProcessingDuration stands in for whatever real work OrderWorkflow
// would do; it just makes the sample's "still processing" -> "completed"
// transition observable via the getOrderState query without needing real
// activities.
const orderProcessingDuration = 10 * time.Second

func main() {
	if err := run(); err != nil {
		log.Fatalf("order-service: %v", err)
	}
}

func run() error {
	envPath := flag.String("env", ".env", "path to a .env file with environment variables (missing file is not an error)")
	flag.Parse()

	if err := dotenv.Load(*envPath); err != nil {
		return fmt.Errorf("load env file %q: %w", *envPath, err)
	}

	namespace := envOr("TEMPORAL_NAMESPACE", "default")
	host := envOr("TEMPORAL_HOST", "localhost:7233")
	workflowTaskQueue := envOr("ORDERS_TASK_QUEUE", "orders-task-queue")
	dispatchTaskQueue := envOr("ORDER_SERVICE_NEXUS_TASK_QUEUE", "order-service-nexus-dispatch")

	c, err := client.Dial(client.Options{HostPort: host, Namespace: namespace})
	if err != nil {
		return fmt.Errorf("dial temporal: %w", err)
	}
	defer c.Close()

	workflowWorker := worker.New(c, workflowTaskQueue, worker.Options{})
	workflowWorker.RegisterWorkflow(OrderWorkflow)

	dispatchWorker := worker.New(c, dispatchTaskQueue, worker.Options{})
	dispatchWorker.RegisterNexusService(newDispatchService(workflowTaskQueue))

	if err := workflowWorker.Start(); err != nil {
		return fmt.Errorf("start OrderWorkflow worker: %w", err)
	}
	defer workflowWorker.Stop()

	if err := dispatchWorker.Start(); err != nil {
		return fmt.Errorf("start dispatch worker: %w", err)
	}
	defer dispatchWorker.Stop()

	log.Printf("order-service: started (namespace=%s, workflow task queue=%s, nexus dispatch task queue=%s)",
		namespace, workflowTaskQueue, dispatchTaskQueue)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()

	log.Println("order-service: shutting down")
	return nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// newDispatchService builds the Nexus service order-service exposes on its
// own dispatchTaskQueue - the same DispatchServiceName/DispatchOperationName
// contract internal/temporal.NewDispatchService exposes for a
// gateway-hosted namespace, so CascadeEvent calls it exactly the same way
// regardless of who's on the other end. Unlike the gateway's version, this
// one validates the payload (see validateOrderPayload) before calling
// ExecuteWorkflow at all: workflowTaskQueue is where the real
// OrderWorkflow run goes, once a payload actually passes that check.
func newDispatchService(workflowTaskQueue string) *nexus.Service {
	operation := nexus.NewSyncOperation(temporal.DispatchOperationName, func(ctx context.Context, input spec.DispatchInput, _ nexus.StartOperationOptions) (spec.DispatchOutput, error) {
		if err := validateOrderPayload(input.Body); err != nil {
			// Reject before a workflow run is ever created: this cascaded
			// event isn't relevant to OrderWorkflow, so there's nothing
			// for a run to do. CascadeEvent records this as this trigger's
			// own error (see internal/temporal.CascadeEvent) without
			// failing the workflow as a whole or affecting any other
			// trigger in the same cascade.
			//
			// This must be HandlerErrorTypeBadRequest, not a plain error:
			// an unclassified error defaults to HandlerErrorTypeInternal,
			// which Temporal treats as retryable - a rejected payload would
			// then be retried indefinitely (and eventually trip the
			// endpoint's circuit breaker) instead of failing once,
			// immediately, the way a validation rejection should.
			return spec.DispatchOutput{}, nexus.NewHandlerErrorf(nexus.HandlerErrorTypeBadRequest, "order-service: rejected cascaded event: %s", err)
		}

		run, err := temporalnexus.GetClient(ctx).ExecuteWorkflow(ctx, client.StartWorkflowOptions{
			ID:        input.Binding.WorkflowID,
			TaskQueue: workflowTaskQueue,
		}, OrderWorkflow, input.Body)
		if err != nil {
			return spec.DispatchOutput{}, err
		}

		return spec.DispatchOutput{Result: map[string]any{
			"workflowId": run.GetID(),
			"runId":      run.GetRunID(),
		}}, nil
	})

	service := nexus.NewService(temporal.DispatchServiceName)
	if err := service.Register(operation); err != nil {
		// Unreachable: the single, constant operation registered here
		// always has a name and is never registered twice.
		panic(err)
	}
	return service
}

// validateOrderPayload is the "not every cascaded event is relevant"
// business rule OrderWorkflow's owner (this service) applies before
// starting anything: an order with no items has nothing for OrderWorkflow
// to fulfill, so it's rejected here rather than spawning a run. This
// stands in for whatever real filtering logic a production order-service
// would need (e.g. checking an event-type discriminator, an allow-listed
// region, entitlement checks, ...) - the point being demonstrated is
// *where* that logic lives: inside the service that owns the workflow, not
// inside the gateway's generic dispatcher (internal/temporal.
// NewDispatchService), which deliberately has no opinion on it.
func validateOrderPayload(body any) error {
	m, ok := body.(map[string]any)
	if !ok {
		return errors.New("payload is not a JSON object")
	}
	if _, ok := m["orderId"].(string); !ok {
		return errors.New("payload is missing orderId")
	}
	items, ok := m["items"].([]any)
	if !ok || len(items) == 0 {
		return errors.New("order has no items - nothing for OrderWorkflow to fulfill")
	}
	return nil
}

// OrderState is OrderWorkflow's queryable state - see the getOrderState
// query (GET /orders/{orderId} in api-spec.yaml).
type OrderState struct {
	Status string `json:"status"`
	Order  any    `json:"order"`
}

// OrderWorkflow is the workflow both the gateway's "direct" driver (POST
// /orders, dispatched straight to Temporal) and its "nexus" driver, via
// this service's own Dispatch operation (POST /orders/{orderId}/cascade),
// start - the same workflow either way, just reached differently. Only the
// nexus path runs validateOrderPayload first, since only that path goes
// through this service's own Nexus operation; the direct path talks to
// Temporal directly and bypasses it entirely.
//
// It tracks a minimal status, answers the getOrderState query, and
// completes either when cancelOrder is signaled (POST
// /orders/{orderId}/cancel) or after simulated processing - whichever
// comes first - so getOrderResult (GET /orders/{orderId}/result) has
// something to block on.
func OrderWorkflow(ctx workflow.Context, order any) (OrderState, error) {
	state := OrderState{Status: "PROCESSING", Order: order}

	if err := workflow.SetQueryHandler(ctx, "getOrderState", func() (OrderState, error) {
		return state, nil
	}); err != nil {
		return state, err
	}

	cancelled := false
	selector := workflow.NewSelector(ctx)

	selector.AddReceive(workflow.GetSignalChannel(ctx, "cancelOrder"), func(c workflow.ReceiveChannel, _ bool) {
		var reason any
		c.Receive(ctx, &reason)
		cancelled = true
	})
	selector.AddFuture(workflow.NewTimer(ctx, orderProcessingDuration), func(workflow.Future) {})

	selector.Select(ctx)

	if cancelled {
		state.Status = "CANCELLED"
		return state, nil
	}
	state.Status = "COMPLETED"
	return state, nil
}
