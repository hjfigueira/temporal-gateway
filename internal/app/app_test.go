package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"go.temporal.io/api/workflowservice/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"

	"temporal-gateway/internal/config"
	"temporal-gateway/internal/telemetry"
	"temporal-gateway/internal/temporal"
)

// fakeFrontend is just enough of a Temporal frontend to dial, pass health
// checks, and hold a getResult long-poll open until the client goes away.
type fakeFrontend struct {
	workflowservice.UnimplementedWorkflowServiceServer
}

func (fakeFrontend) GetSystemInfo(context.Context, *workflowservice.GetSystemInfoRequest) (*workflowservice.GetSystemInfoResponse, error) {
	return &workflowservice.GetSystemInfoResponse{}, nil
}

func (fakeFrontend) GetWorkflowExecutionHistory(ctx context.Context, _ *workflowservice.GetWorkflowExecutionHistoryRequest) (*workflowservice.GetWorkflowExecutionHistoryResponse, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func startFakeFrontend(t *testing.T) string {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer()
	workflowservice.RegisterWorkflowServiceServer(srv, fakeFrontend{})
	hs := health.NewServer()
	hs.SetServingStatus("temporal.api.workflowservice.v1.WorkflowService", healthpb.HealthCheckResponse_SERVING)
	healthpb.RegisterHealthServer(srv, hs)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	return lis.Addr().String()
}

// freePort returns a loopback port that was free a moment ago.
func freePort(t *testing.T) int {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lis.Close() }()
	return lis.Addr().(*net.TCPAddr).Port
}

// gatewayFiles describes the config.yml + spec.yaml a test runs against.
type gatewayFiles struct {
	temporalHost string
	apiPort      int
	healthPort   int // 0 disables the probe server
	otel         string
	specNS       string // namespace the spec's trigger targets
	unlimited    bool   // retry the Temporal dial forever (default: 1 attempt)
	spec         string // overrides the whole spec when set
}

func (g gatewayFiles) write(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if g.otel == "" {
		g.otel = "{enabled: false}"
	}
	if g.specNS == "" {
		g.specNS = "default"
	}
	maxAttempts := 1
	if g.unlimited {
		maxAttempts = 0
	}
	healthCfg := "{enabled: false}"
	if g.healthPort != 0 {
		healthCfg = fmt.Sprintf("{enabled: true, host: 127.0.0.1, port: %d}", g.healthPort)
	}
	cfg := fmt.Sprintf(`server: {host: 127.0.0.1, port: %d}
health: %s
otel: %s
middlewares: [{name: logging, enabled: true}]
temporal:
  reconnect: {maxAttempts: %d, interval: 50ms}
  connections:
    - namespace: default
      host: %q
      workflows: [{name: OrderWorkflow, taskQueue: orders}]
apiSpec: ./spec.yaml
`, g.apiPort, healthCfg, g.otel, maxAttempts, g.temporalHost)
	spec := g.spec
	if spec == "" {
		spec = fmt.Sprintf(`openapi: 3.0.3
info: {title: T, version: "1"}
paths:
  /orders/{orderId}/result:
    get:
      operationId: getResult
      x-temporal:
        triggers:
          - {action: getResult, namespace: %s, workflowId: "order-{path.orderId}"}
`, g.specNS)
	}
	for name, body := range map[string]string{"config.yml": cfg, "spec.yaml": spec} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(dir, "config.yml")
}

// syncBuffer is a goroutine-safe log sink.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func newLogger() (*slog.Logger, *syncBuffer) {
	buf := &syncBuffer{}
	return slog.New(slog.NewTextHandler(buf, nil)), buf
}

// run drives the same modules, in the same order, as main.
func run(ctx context.Context, args []string, logger *slog.Logger) error {
	return runWith(ctx, args, logger, 10*time.Second, telemetry.Setup)
}

func runWith(ctx context.Context, args []string, logger *slog.Logger, shutdownTimeout time.Duration, setupTelemetry func(context.Context, config.OTelConfig) (func(context.Context) error, error)) error {
	return Run(ctx, &State{Args: args, Logger: logger, ShutdownTimeout: shutdownTimeout},
		ParseFlags(), LoadDotEnv(), LoadConfig(), Telemetry(setupTelemetry), LoadSpec(),
		LogStartup(), Temporal(temporal.NewClient), DryRun(), Health(), API())
}

func runArgs(configPath string, extra ...string) []string {
	return append([]string{"-config", configPath, "-env", "/nonexistent/.env"}, extra...)
}

func TestRunStartupFailures(t *testing.T) {
	temporalHost := startFakeFrontend(t)

	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = busy.Close() })
	busyPort := busy.Addr().(*net.TCPAddr).Port

	tests := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{name: "unknown flag", args: []string{"-bogus"}, wantErr: "bogus"},
		{name: "unreadable env file", args: []string{"-env", t.TempDir()}, wantErr: "load env file"},
		{name: "missing config", args: runArgs(filepath.Join(t.TempDir(), "nope.yml")), wantErr: "load gateway config"},
		{name: "bad telemetry endpoint", args: runArgs(gatewayFiles{temporalHost: temporalHost, otel: `{enabled: true, endpoint: "%%%"}`}.write(t)), wantErr: "set up telemetry"},
		{name: "bad api spec", args: runArgs(gatewayFiles{temporalHost: temporalHost, spec: "paths: ["}.write(t)), wantErr: "load api spec"},
		{name: "health port taken", args: runArgs(gatewayFiles{temporalHost: temporalHost, healthPort: busyPort}.write(t)), wantErr: "start health server"},
		{name: "temporal unreachable", args: runArgs(gatewayFiles{temporalHost: "127.0.0.1:1"}.write(t), "-dry-run"), wantErr: "connect to temporal"},
		{name: "spec names an unknown namespace", args: runArgs(gatewayFiles{temporalHost: temporalHost, specNS: "elsewhere"}.write(t), "-dry-run"), wantErr: "has no temporal.connections entry"},
		{name: "dial gives up while serving", args: runArgs(gatewayFiles{temporalHost: "127.0.0.1:1", apiPort: freePort(t)}.write(t)), wantErr: "connect to temporal"},
		{name: "api port taken", args: runArgs(gatewayFiles{temporalHost: temporalHost, apiPort: busyPort}.write(t)), wantErr: "http server stopped"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger, _ := newLogger()
			err := run(context.Background(), tt.args, logger)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("run err = %v, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

func TestRunHelpAndDryRunSucceed(t *testing.T) {
	logger, _ := newLogger()
	if err := run(context.Background(), []string{"-h"}, logger); err != nil {
		t.Fatalf("-h: %v", err)
	}
	cfg := gatewayFiles{temporalHost: startFakeFrontend(t)}.write(t)
	if err := run(context.Background(), runArgs(cfg, "-dry-run"), logger); err != nil {
		t.Fatalf("-dry-run: %v", err)
	}
}

// TestRunServesAndShutsDown runs the whole gateway: probes and API come up,
// then a shutdown with a request still in flight fails the graceful drain
// (logged, not fatal) and a telemetry flush failure is logged too.
func TestRunServesAndShutsDown(t *testing.T) {
	failingFlush := func(context.Context, config.OTelConfig) (func(context.Context) error, error) {
		return func(context.Context) error { return errors.New("flush failed") }, nil
	}

	apiPort, healthPort := freePort(t), freePort(t)
	cfg := gatewayFiles{temporalHost: startFakeFrontend(t), apiPort: apiPort, healthPort: healthPort}.write(t)
	logger, logs := newLogger()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runWith(ctx, runArgs(cfg), logger, 100*time.Millisecond, failingFlush) }()

	waitFor(t, fmt.Sprintf("http://127.0.0.1:%d/readyz", healthPort), http.StatusOK)

	// A getResult that never completes keeps a request in flight.
	inFlight := make(chan struct{})
	go func() {
		defer close(inFlight)
		resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/orders/o1/result", apiPort))
		if err == nil {
			_ = resp.Body.Close()
		}
	}()
	time.Sleep(200 * time.Millisecond)

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("run did not return after cancellation")
	}
	<-inFlight

	for _, want := range []string{"graceful shutdown failed", "failed to shut down telemetry", "route registered", "workflow registered"} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("logs missing %q:\n%s", want, logs.String())
		}
	}
}

func waitFor(t *testing.T, url string, status int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if resp, err := http.Get(url); err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode == status {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("%s never returned %d", url, status)
}

// TestRunServesBeforeTemporalConnects: with Temporal unreachable (and
// retried forever), the API is already up and answers 503, readiness says
// which namespace is pending, and a shutdown signal still exits cleanly.
func TestRunServesBeforeTemporalConnects(t *testing.T) {
	apiPort, healthPort := freePort(t), freePort(t)
	cfg := gatewayFiles{temporalHost: "127.0.0.1:1", apiPort: apiPort, healthPort: healthPort, unlimited: true}.write(t)
	logger, _ := newLogger()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- run(ctx, runArgs(cfg), logger) }()

	waitFor(t, fmt.Sprintf("http://127.0.0.1:%d/livez", healthPort), http.StatusOK)
	waitFor(t, fmt.Sprintf("http://127.0.0.1:%d/orders/o1/result", apiPort), http.StatusServiceUnavailable)

	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/readyz", healthPort))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable || !strings.Contains(string(body), "not connected yet") {
		t.Fatalf("/readyz = %d %s, want 503 naming the pending namespace", resp.StatusCode, body)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run after shutdown signal: %v, want nil", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("run did not return after cancellation")
	}
}

// TestRunLoadsDotEnvBeforeConfig: a .env value (here with an inline comment,
// which godotenv strips) feeds ${VAR} expansion in config.yml.
func TestRunLoadsDotEnvBeforeConfig(t *testing.T) {
	addr := startFakeFrontend(t)
	cfg := gatewayFiles{temporalHost: "${TG_TEST_TEMPORAL_HOST}"}.write(t)
	envFile := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(envFile, []byte("TG_TEST_TEMPORAL_HOST="+addr+" # fake frontend\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Unsetenv("TG_TEST_TEMPORAL_HOST") })

	logger, _ := newLogger()
	if err := run(context.Background(), []string{"-config", cfg, "-env", envFile, "-dry-run"}, logger); err != nil {
		t.Fatalf("run: %v", err)
	}
}

// TestRunLifecycleOrder: Inits run in order, ErrDone ends startup cleanly,
// and only modules whose Init succeeded are stopped, in reverse.
func TestRunLifecycleOrder(t *testing.T) {
	var got []string
	mod := func(name string, initErr error) Module {
		return Module{
			Init: func(context.Context, *State) error { got = append(got, "init "+name); return initErr },
			Run:  func(context.Context, *State) error { got = append(got, "run "+name); return nil },
			Stop: func(context.Context, *State) { got = append(got, "stop "+name) },
		}
	}
	if err := Run(context.Background(), &State{}, mod("a", nil), mod("b", ErrDone), mod("c", nil)); err != nil {
		t.Fatalf("ErrDone: %v, want nil", err)
	}
	want := "init a,init b,stop b,stop a"
	if strings.Join(got, ",") != want {
		t.Fatalf("got %v, want %s", got, want)
	}

	got = nil
	boom := errors.New("boom")
	if err := Run(context.Background(), &State{}, mod("a", nil), mod("b", boom), mod("c", nil)); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
	if want := "init a,init b,stop a"; strings.Join(got, ",") != want {
		t.Fatalf("got %v, want %s", got, want)
	}
}

// TestRunFirstRunErrorCancelsOthers: a failing Run cancels its siblings, and
// their resulting errors don't replace the cause.
func TestRunFirstRunErrorCancelsOthers(t *testing.T) {
	boom := errors.New("boom")
	failing := Module{Run: func(context.Context, *State) error { return boom }}
	waiting := Module{Run: func(ctx context.Context, _ *State) error { <-ctx.Done(); return ctx.Err() }}
	if err := Run(context.Background(), &State{}, waiting, failing); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
}
