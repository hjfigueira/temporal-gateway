package temporal

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.temporal.io/sdk/client"

	"temporal-gateway/internal/config"
)

var errUnavailable = errors.New("connection refused")

// fakeDial fails the first failures calls, then succeeds, counting calls.
func fakeDial(failures int, calls *int) func(context.Context) (client.Client, error) {
	return func(context.Context) (client.Client, error) {
		*calls++
		if *calls <= failures {
			return nil, errUnavailable
		}
		return nil, nil
	}
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestDialWithRetry_SucceedsAfterFailures(t *testing.T) {
	var calls int
	_, err := dialWithRetry(context.Background(), fakeDial(3, &calls), time.Millisecond, 0, discardLogger())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 4 {
		t.Fatalf("calls = %d, want 4", calls)
	}
}

func TestDialWithRetry_GivesUpAfterMaxAttempts(t *testing.T) {
	var calls int
	_, err := dialWithRetry(context.Background(), fakeDial(10, &calls), time.Millisecond, 3, discardLogger())
	if !errors.Is(err, errUnavailable) {
		t.Fatalf("err = %v, want wrapping %v", err, errUnavailable)
	}
	if calls != 3 {
		t.Fatalf("calls = %d, want 3", calls)
	}
}

func TestDialWithRetry_SingleAttemptDoesNotWait(t *testing.T) {
	var calls int
	start := time.Now()
	_, err := dialWithRetry(context.Background(), fakeDial(10, &calls), time.Hour, 1, discardLogger())
	if err == nil || calls != 1 {
		t.Fatalf("err = %v, calls = %d; want an error after 1 call", err, calls)
	}
	if time.Since(start) > time.Second {
		t.Fatal("single-attempt dial waited for the retry interval")
	}
}

func TestDialWithRetry_StopsWhenCancelledDuringDial(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	dial := func(context.Context) (client.Client, error) {
		cancel() // e.g. SIGTERM arrives while the dial is in flight
		return nil, errUnavailable
	}
	if _, err := dialWithRetry(ctx, dial, time.Hour, 0, discardLogger()); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestDialWithRetry_StopsWhenCancelledWhileWaiting(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	dialed := make(chan struct{}, 1)
	dial := func(context.Context) (client.Client, error) {
		dialed <- struct{}{}
		return nil, errUnavailable
	}
	done := make(chan error, 1)
	go func() {
		_, err := dialWithRetry(ctx, dial, time.Hour, 0, discardLogger())
		done <- err
	}()
	<-dialed // the first attempt failed; dialWithRetry is now waiting an hour
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("dialWithRetry did not return after context cancellation")
	}
}

func TestTLSConfig_CABundle(t *testing.T) {
	dir := t.TempDir()

	bad := filepath.Join(dir, "bad.pem")
	if err := os.WriteFile(bad, []byte("not a certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := tlsConfig(config.TemporalTLSConfig{Enabled: true, CAPath: bad}); err == nil {
		t.Fatal("tlsConfig with a non-PEM CA bundle: want error, got nil")
	}
	if _, err := tlsConfig(config.TemporalTLSConfig{Enabled: true, CAPath: filepath.Join(dir, "missing.pem")}); err == nil {
		t.Fatal("tlsConfig with a missing CA bundle: want error, got nil")
	}

	got, err := tlsConfig(config.TemporalTLSConfig{Enabled: true, ServerName: "temporal.internal"})
	if err != nil {
		t.Fatal(err)
	}
	if got.ServerName != "temporal.internal" || got.RootCAs != nil {
		t.Fatalf("tlsConfig = {ServerName: %q, RootCAs: %v}, want server name set and system roots", got.ServerName, got.RootCAs)
	}
}
