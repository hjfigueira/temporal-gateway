package temporal

import (
	"context"

	"go.temporal.io/api/workflowservice/v1"
	"google.golang.org/grpc"
)

// startedKey carries a *bool through ExecuteWorkflow's context so
// startedInterceptor can report whether the server actually created a new
// run. The SDK reads StartWorkflowExecutionResponse.Started but doesn't
// expose it on client.WorkflowRun, and comparing RunIDs against a prior
// DescribeWorkflowExecution is racy (two concurrent starts both see "no
// prior run") - see ADR-021.
type startedKey struct{}

// withStartedFlag returns a context whose StartWorkflowExecution call (if
// any) records the server's Started flag into the returned *bool. It stays
// false when the call fails, including the AlreadyStarted error the SDK
// swallows when WorkflowExecutionErrorWhenAlreadyStarted is false - which
// is correct, since no new run was created then either.
func withStartedFlag(ctx context.Context) (context.Context, *bool) {
	started := new(bool)
	return context.WithValue(ctx, startedKey{}, started), started
}

// startedInterceptor is a gRPC unary client interceptor that copies a
// successful StartWorkflowExecutionResponse's Started flag into the *bool
// withStartedFlag put on the call's context.
func startedInterceptor(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
	err := invoker(ctx, method, req, reply, cc, opts...)
	if err != nil {
		return err
	}
	if resp, ok := reply.(*workflowservice.StartWorkflowExecutionResponse); ok {
		if started, ok := ctx.Value(startedKey{}).(*bool); ok {
			*started = resp.GetStarted()
		}
	}
	return nil
}
