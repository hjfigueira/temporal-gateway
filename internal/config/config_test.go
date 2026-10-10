package config

import (
	"testing"
	"time"
)

func TestTemporalReconnectConfig(t *testing.T) {
	tests := []struct {
		name     string
		cfg      TemporalReconnectConfig
		wantErr  bool
		interval time.Duration
	}{
		{name: "defaults", cfg: TemporalReconnectConfig{}, interval: 5 * time.Second},
		{name: "custom interval", cfg: TemporalReconnectConfig{Interval: "250ms", MaxAttempts: 10}, interval: 250 * time.Millisecond},
		{name: "malformed interval", cfg: TemporalReconnectConfig{Interval: "soon"}, wantErr: true},
		{name: "zero interval", cfg: TemporalReconnectConfig{Interval: "0s"}, wantErr: true},
		{name: "negative max attempts", cfg: TemporalReconnectConfig{MaxAttempts: -1}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.validate()
			if (err != nil) != tt.wantErr {
				t.Fatalf("validate() err = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && tt.cfg.IntervalDuration() != tt.interval {
				t.Fatalf("IntervalDuration() = %v, want %v", tt.cfg.IntervalDuration(), tt.interval)
			}
		})
	}
}

func TestHealthConfigValidate(t *testing.T) {
	server := ServerConfig{Port: 8081}
	tests := []struct {
		name    string
		cfg     HealthConfig
		wantErr bool
	}{
		{name: "disabled ignores port", cfg: HealthConfig{Enabled: false}},
		{name: "valid", cfg: HealthConfig{Enabled: true, Port: 8082}},
		{name: "port out of range", cfg: HealthConfig{Enabled: true, Port: 0}, wantErr: true},
		{name: "same port as server", cfg: HealthConfig{Enabled: true, Port: 8081}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.cfg.validate(server); (err != nil) != tt.wantErr {
				t.Fatalf("validate() err = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestServerConfig(t *testing.T) {
	tests := []struct {
		name       string
		cfg        ServerConfig
		wantErr    bool
		maxBody    int64
		reqTimeout time.Duration
	}{
		{name: "defaults", cfg: ServerConfig{}, maxBody: 1 << 20, reqTimeout: 25 * time.Second},
		{name: "custom", cfg: ServerConfig{MaxBodyBytes: 512, RequestTimeout: "2m"}, maxBody: 512, reqTimeout: 2 * time.Minute},
		{name: "negative body limit", cfg: ServerConfig{MaxBodyBytes: -1}, wantErr: true},
		{name: "malformed timeout", cfg: ServerConfig{RequestTimeout: "soon"}, wantErr: true},
		{name: "zero timeout", cfg: ServerConfig{RequestTimeout: "0s"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.validate()
			if (err != nil) != tt.wantErr {
				t.Fatalf("validate() err = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if got := tt.cfg.MaxBodyBytesOrDefault(); got != tt.maxBody {
				t.Errorf("MaxBodyBytesOrDefault() = %d, want %d", got, tt.maxBody)
			}
			if got := tt.cfg.RequestTimeoutDuration(); got != tt.reqTimeout {
				t.Errorf("RequestTimeoutDuration() = %v, want %v", got, tt.reqTimeout)
			}
		})
	}
}

func TestTemporalConfigTLSKeypairPairing(t *testing.T) {
	conn := func(tls TemporalTLSConfig) TemporalConfig {
		return TemporalConfig{Connections: []TemporalConnectionConfig{{Namespace: "default", TLS: tls}}}
	}
	if err := conn(TemporalTLSConfig{Enabled: true, CertPath: "c.pem", KeyPath: "k.pem", CAPath: "ca.pem"}).validate(); err != nil {
		t.Fatalf("validate() with full keypair: %v", err)
	}
	if err := conn(TemporalTLSConfig{Enabled: true, CAPath: "ca.pem"}).validate(); err != nil {
		t.Fatalf("validate() with CA only: %v", err)
	}
	if err := conn(TemporalTLSConfig{Enabled: true, CertPath: "c.pem"}).validate(); err == nil {
		t.Fatal("validate() with certPath but no keyPath: want error, got nil")
	}
}
