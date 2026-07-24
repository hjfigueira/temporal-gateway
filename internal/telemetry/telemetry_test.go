package telemetry

import (
	"context"
	"testing"

	"temporal-gateway/internal/config"
)

func TestSampleRatioDefaultsToOne(t *testing.T) {
	tests := []struct {
		name  string
		ratio float64
		want  float64
	}{
		{"unset defaults to 1", 0, 1},
		{"negative defaults to 1", -0.5, 1},
		{"valid ratio passes through", 0.25, 0.25},
		{"1 passes through", 1, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sampleRatio(config.OTelConfig{SampleRatio: tt.ratio})
			if got != tt.want {
				t.Errorf("sampleRatio(%v) = %v, want %v", tt.ratio, got, tt.want)
			}
		})
	}
}

func TestSetupDisabledReturnsNoopShutdown(t *testing.T) {
	shutdown, err := Setup(context.Background(), config.OTelConfig{Enabled: false})
	if err != nil {
		t.Fatalf("Setup returned error: %v", err)
	}
	if shutdown == nil {
		t.Fatal("expected a non-nil shutdown func even when disabled")
	}
	if err := shutdown(context.Background()); err != nil {
		t.Errorf("no-op shutdown returned error: %v", err)
	}
}
