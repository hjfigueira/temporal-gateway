package temporal

import (
	"context"
	"errors"
	"testing"

	"go.temporal.io/api/workflowservice/v1"
	"google.golang.org/grpc"
)

func TestStartedInterceptor(t *testing.T) {
	invoker := func(started bool, err error) grpc.UnaryInvoker {
		return func(_ context.Context, _ string, _, reply any, _ *grpc.ClientConn, _ ...grpc.CallOption) error {
			if err != nil {
				return err
			}
			reply.(*workflowservice.StartWorkflowExecutionResponse).Started = started
			return nil
		}
	}

	tests := []struct {
		name    string
		started bool
		err     error
		want    bool
	}{
		{name: "server created a new run", started: true, want: true},
		{name: "server attached to an existing run", started: false, want: false},
		{name: "call failed", err: errors.New("already started"), want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, started := withStartedFlag(context.Background())
			err := startedInterceptor(ctx, "/StartWorkflowExecution", nil, &workflowservice.StartWorkflowExecutionResponse{}, nil, invoker(tt.started, tt.err))
			if !errors.Is(err, tt.err) {
				t.Fatalf("err = %v, want %v", err, tt.err)
			}
			if *started != tt.want {
				t.Fatalf("started = %v, want %v", *started, tt.want)
			}
		})
	}
}

func TestStartedInterceptor_IgnoresOtherCallsAndContexts(t *testing.T) {
	ok := func(context.Context, string, any, any, *grpc.ClientConn, ...grpc.CallOption) error { return nil }

	ctx, started := withStartedFlag(context.Background())
	if err := startedInterceptor(ctx, "/SignalWorkflowExecution", nil, &workflowservice.SignalWorkflowExecutionResponse{}, nil, ok); err != nil {
		t.Fatal(err)
	}
	if *started {
		t.Fatal("started set by a non-start call")
	}

	// No flag on the context: must not panic.
	if err := startedInterceptor(context.Background(), "/StartWorkflowExecution", nil, &workflowservice.StartWorkflowExecutionResponse{Started: true}, nil, ok); err != nil {
		t.Fatal(err)
	}
}
