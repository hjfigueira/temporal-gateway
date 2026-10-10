package temporal

import (
	"context"
	"net"
	"sync"
	"testing"

	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	historypb "go.temporal.io/api/history/v1"
	querypb "go.temporal.io/api/query/v1"
	"go.temporal.io/api/serviceerror"
	workflowpb "go.temporal.io/api/workflow/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"

	"temporal-gateway/internal/config"
)

// fakeFrontend is an in-process Temporal frontend: just enough of
// WorkflowService (plus gRPC health) for the real SDK client to dial it and
// run every action the dispatcher uses. Behavior is set per test through
// its fields, guarded by mu since the gRPC handlers run on other goroutines.
type fakeFrontend struct {
	workflowservice.UnimplementedWorkflowServiceServer

	mu sync.Mutex
	// err, when set, fails every action RPC with it.
	err error
	// started is the StartWorkflowExecutionResponse.Started the server reports.
	started bool
	// describeErr fails DescribeWorkflowExecution; otherwise it reports
	// describeStatus.
	describeErr    error
	describeStatus enumspb.WorkflowExecutionStatus
	// result is returned by queries and as the completed workflow's result.
	result *commonpb.Payloads
	// failWorkflow makes getResult see a failed workflow.
	failWorkflow bool
	// lastStart is the most recent start request.
	lastStart *workflowservice.StartWorkflowExecutionRequest
}

// startFakeFrontend serves a fakeFrontend on a loopback port and returns it
// with its address; both are torn down when t ends.
func startFakeFrontend(t *testing.T) (*fakeFrontend, string) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeFrontend{started: true, result: mustPayloads(t, map[string]any{"ok": true})}
	srv := grpc.NewServer()
	workflowservice.RegisterWorkflowServiceServer(srv, f)
	hs := health.NewServer()
	hs.SetServingStatus("temporal.api.workflowservice.v1.WorkflowService", healthpb.HealthCheckResponse_SERVING)
	healthpb.RegisterHealthServer(srv, hs)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	return f, lis.Addr().String()
}

// dialFake dials a real SDK client at addr for namespace "default".
func dialFake(t *testing.T, addr string) client.Client {
	t.Helper()
	c, err := NewClient(context.Background(), config.TemporalConnectionConfig{Namespace: "default", Host: addr}, config.TemporalReconnectConfig{MaxAttempts: 1}, discardLogger())
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	t.Cleanup(c.Close)
	return c
}

func mustPayloads(t *testing.T, v any) *commonpb.Payloads {
	t.Helper()
	p, err := converter.GetDefaultDataConverter().ToPayloads(v)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func (f *fakeFrontend) set(fn func(f *fakeFrontend)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(f)
}

// fail returns f.err as a gRPC status error, the way a real frontend would.
func (f *fakeFrontend) fail() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err == nil {
		return nil
	}
	return serviceerror.ToStatus(f.err).Err()
}

func (f *fakeFrontend) GetSystemInfo(context.Context, *workflowservice.GetSystemInfoRequest) (*workflowservice.GetSystemInfoResponse, error) {
	return &workflowservice.GetSystemInfoResponse{}, nil
}

func (f *fakeFrontend) StartWorkflowExecution(_ context.Context, req *workflowservice.StartWorkflowExecutionRequest) (*workflowservice.StartWorkflowExecutionResponse, error) {
	if err := f.fail(); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastStart = req
	return &workflowservice.StartWorkflowExecutionResponse{RunId: "run-1", Started: f.started}, nil
}

func (f *fakeFrontend) DescribeWorkflowExecution(context.Context, *workflowservice.DescribeWorkflowExecutionRequest) (*workflowservice.DescribeWorkflowExecutionResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.describeErr != nil {
		return nil, serviceerror.ToStatus(f.describeErr).Err()
	}
	return &workflowservice.DescribeWorkflowExecutionResponse{
		WorkflowExecutionInfo: &workflowpb.WorkflowExecutionInfo{Status: f.describeStatus},
	}, nil
}

func (f *fakeFrontend) SignalWorkflowExecution(context.Context, *workflowservice.SignalWorkflowExecutionRequest) (*workflowservice.SignalWorkflowExecutionResponse, error) {
	if err := f.fail(); err != nil {
		return nil, err
	}
	return &workflowservice.SignalWorkflowExecutionResponse{}, nil
}

func (f *fakeFrontend) RequestCancelWorkflowExecution(context.Context, *workflowservice.RequestCancelWorkflowExecutionRequest) (*workflowservice.RequestCancelWorkflowExecutionResponse, error) {
	if err := f.fail(); err != nil {
		return nil, err
	}
	return &workflowservice.RequestCancelWorkflowExecutionResponse{}, nil
}

func (f *fakeFrontend) TerminateWorkflowExecution(context.Context, *workflowservice.TerminateWorkflowExecutionRequest) (*workflowservice.TerminateWorkflowExecutionResponse, error) {
	if err := f.fail(); err != nil {
		return nil, err
	}
	return &workflowservice.TerminateWorkflowExecutionResponse{}, nil
}

func (f *fakeFrontend) QueryWorkflow(context.Context, *workflowservice.QueryWorkflowRequest) (*workflowservice.QueryWorkflowResponse, error) {
	if err := f.fail(); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return &workflowservice.QueryWorkflowResponse{QueryResult: f.result, QueryRejected: (*querypb.QueryRejected)(nil)}, nil
}

func (f *fakeFrontend) GetWorkflowExecutionHistory(context.Context, *workflowservice.GetWorkflowExecutionHistoryRequest) (*workflowservice.GetWorkflowExecutionHistoryResponse, error) {
	if err := f.fail(); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	event := &historypb.HistoryEvent{
		EventId:   1,
		EventType: enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_COMPLETED,
		Attributes: &historypb.HistoryEvent_WorkflowExecutionCompletedEventAttributes{
			WorkflowExecutionCompletedEventAttributes: &historypb.WorkflowExecutionCompletedEventAttributes{Result: f.result},
		},
	}
	if f.failWorkflow {
		event = &historypb.HistoryEvent{
			EventId:   1,
			EventType: enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_TERMINATED,
			Attributes: &historypb.HistoryEvent_WorkflowExecutionTerminatedEventAttributes{
				WorkflowExecutionTerminatedEventAttributes: &historypb.WorkflowExecutionTerminatedEventAttributes{Reason: "stopped"},
			},
		}
	}
	return &workflowservice.GetWorkflowExecutionHistoryResponse{History: &historypb.History{Events: []*historypb.HistoryEvent{event}}}, nil
}
