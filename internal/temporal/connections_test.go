package temporal

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"

	"temporal-gateway/internal/config"
	"temporal-gateway/internal/spec"
)

// stubClient stands in for a dialed client where no RPC is made.
type stubClient struct{ client.Client }

func (stubClient) Close() {}

// connected returns a Connection whose client is already set.
func connected(cl client.Client, catalog *Catalog) *Connection {
	conn := &Connection{Catalog: catalog}
	conn.setClient(cl)
	return conn
}

func TestConnect_DialsConcurrentlyAndReportsEveryFailure(t *testing.T) {
	const dialTime = 100 * time.Millisecond
	dial := func(ctx context.Context, c config.TemporalConnectionConfig, _ config.TemporalReconnectConfig, _ *slog.Logger) (client.Client, error) {
		time.Sleep(dialTime)
		if strings.HasPrefix(c.Namespace, "down") {
			return nil, errUnavailable
		}
		return stubClient{}, nil
	}

	cfg := config.TemporalConfig{Connections: []config.TemporalConnectionConfig{
		{Namespace: "default"}, {Namespace: "down-a"}, {Namespace: "down-b"},
	}}
	conns := NewConnections(cfg, dial)
	if len(conns) != 3 || conns["default"].Client() != nil {
		t.Fatalf("NewConnections = %v, want 3 entries, none connected yet", conns)
	}

	start := time.Now()
	err := conns.Connect(context.Background(), cfg.Reconnect, discardLogger())
	if elapsed := time.Since(start); elapsed >= 2*dialTime {
		t.Errorf("Connect took %v, want under %v (dials should run concurrently)", elapsed, 2*dialTime)
	}

	if !errors.Is(err, errUnavailable) || !strings.Contains(err.Error(), `"down-a"`) || !strings.Contains(err.Error(), `"down-b"`) {
		t.Fatalf("err = %v, want both failed namespaces reported", err)
	}
	if conns["default"].Client() == nil || conns["down-a"].Client() != nil {
		t.Fatal("want only the reachable namespace connected")
	}
	conns.Close() // closes "default", skips the never-connected ones
}

func TestNotYetConnectedNamespace(t *testing.T) {
	conns := NewConnections(config.TemporalConfig{Connections: []config.TemporalConnectionConfig{{Namespace: "default"}}}, NewClient)

	_, err := NewDispatcher(conns).Dispatch(context.Background(), spec.TemporalBinding{Action: spec.ActionCancelWorkflow, Namespace: "default"}, "wf", nil)
	var unavailable *serviceerror.Unavailable
	if !errors.As(err, &unavailable) {
		t.Fatalf("Dispatch err = %v, want serviceerror.Unavailable (answered 503)", err)
	}

	if got := conns.CheckHealth(context.Background()); !errors.Is(got["default"], errNotConnected) {
		t.Fatalf("CheckHealth = %v, want default not connected", got)
	}
}

func TestValidateBindingsTaskQueueFromCatalog(t *testing.T) {
	conns := NewConnections(config.TemporalConfig{Connections: []config.TemporalConnectionConfig{{
		Namespace: "default",
		Workflows: []config.WorkflowDefinition{{Name: "OrderWorkflow", TaskQueue: "orders"}},
	}}}, NewClient)
	start := func(workflowType, taskQueue string) spec.TemporalBinding {
		return spec.TemporalBinding{Action: spec.ActionStartWorkflow, Namespace: "default", WorkflowType: workflowType, TaskQueue: taskQueue}
	}
	apiSpec := &spec.Spec{Paths: map[string]spec.PathItem{"/x": {Post: &spec.Operation{Temporal: spec.TemporalSpec{Triggers: []spec.TemporalBinding{
		start("OrderWorkflow", ""),                                // catalog supplies it
		start("OtherWorkflow", "others"),                          // explicit
		start("OtherWorkflow", ""),                                // neither
		{Action: spec.ActionSignalWorkflow, Namespace: "default"}, // not a start
	}}}}}}

	err := ValidateBindings(apiSpec, conns)
	if err == nil || !strings.Contains(err.Error(), "triggers[2]") || strings.Contains(err.Error(), "triggers[0]") || strings.Contains(err.Error(), "triggers[1]") || strings.Contains(err.Error(), "triggers[3]") {
		t.Fatalf("err = %v, want only triggers[2] reported", err)
	}
}
