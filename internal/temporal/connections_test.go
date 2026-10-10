package temporal

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"go.temporal.io/sdk/client"

	"temporal-gateway/internal/config"
)

func TestNewConnections_DialsConcurrentlyAndReportsEveryFailure(t *testing.T) {
	const dialTime = 100 * time.Millisecond
	newClient = func(ctx context.Context, c config.TemporalConnectionConfig, _ config.TemporalReconnectConfig, _ *slog.Logger) (client.Client, error) {
		time.Sleep(dialTime)
		if strings.HasPrefix(c.Namespace, "down") {
			return nil, errUnavailable
		}
		return nil, nil
	}
	t.Cleanup(func() { newClient = NewClient })

	cfg := config.TemporalConfig{Connections: []config.TemporalConnectionConfig{
		{Namespace: "default"}, {Namespace: "down-a"}, {Namespace: "down-b"},
	}}

	start := time.Now()
	conns, err := NewConnections(context.Background(), cfg, discardLogger())
	if elapsed := time.Since(start); elapsed >= 2*dialTime {
		t.Errorf("NewConnections took %v, want under %v (dials should run concurrently)", elapsed, 2*dialTime)
	}

	if !errors.Is(err, errUnavailable) || !strings.Contains(err.Error(), `"down-a"`) || !strings.Contains(err.Error(), `"down-b"`) {
		t.Fatalf("err = %v, want both failed namespaces reported", err)
	}
	if _, ok := conns["default"]; !ok || len(conns) != 1 {
		t.Fatalf("conns = %v, want only the successful namespace (so the caller can close it)", conns)
	}
}
