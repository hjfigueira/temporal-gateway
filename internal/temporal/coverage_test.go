package temporal

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	enumspb "go.temporal.io/api/enums/v1"

	"temporal-gateway/internal/config"
	"temporal-gateway/internal/spec"
)

// writeSelfSignedKeypair writes a self-signed cert and its key as PEM files
// and returns their paths. The cert doubles as a CA bundle.
func writeSelfSignedKeypair(t *testing.T) (certPath, keyPath string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		IsCA:         true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	certPath, keyPath = filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	return certPath, keyPath
}

func TestTLSConfigKeypairAndCA(t *testing.T) {
	certPath, keyPath := writeSelfSignedKeypair(t)

	got, err := tlsConfig(config.TemporalTLSConfig{Enabled: true, CertPath: certPath, KeyPath: keyPath, CAPath: certPath})
	if err != nil {
		t.Fatalf("tlsConfig: %v", err)
	}
	if len(got.Certificates) != 1 || got.RootCAs == nil {
		t.Fatalf("tlsConfig = %d certificates, RootCAs %v; want the keypair and the CA pool", len(got.Certificates), got.RootCAs)
	}

	if _, err := tlsConfig(config.TemporalTLSConfig{Enabled: true, CertPath: certPath, KeyPath: certPath}); err == nil {
		t.Fatal("tlsConfig with a cert as its own key: want an error")
	}
}

func TestClientOptionsTLSAndAPIKey(t *testing.T) {
	certPath, _ := writeSelfSignedKeypair(t)

	opts, err := clientOptions(config.TemporalConnectionConfig{
		Namespace: "ns", Host: "h:7233", APIKey: "secret",
		TLS: config.TemporalTLSConfig{Enabled: true, CAPath: certPath, ServerName: "temporal.internal"},
	})
	if err != nil {
		t.Fatalf("clientOptions: %v", err)
	}
	if opts.ConnectionOptions.TLS == nil || opts.ConnectionOptions.TLS.ServerName != "temporal.internal" {
		t.Errorf("TLS = %+v, want server name set", opts.ConnectionOptions.TLS)
	}
	if opts.Credentials == nil {
		t.Error("Credentials = nil, want API-key credentials")
	}
	if len(opts.ConnectionOptions.DialOptions) != 1 || len(opts.Interceptors) != 1 {
		t.Errorf("want the started dial option and the tracing interceptor, got %d / %d", len(opts.ConnectionOptions.DialOptions), len(opts.Interceptors))
	}
}

func TestNewClientErrors(t *testing.T) {
	// A bad CA is a configuration error: returned at once, never dialed.
	badTLS := config.TemporalConnectionConfig{Namespace: "ns", Host: "127.0.0.1:1", TLS: config.TemporalTLSConfig{Enabled: true, CAPath: filepath.Join(t.TempDir(), "missing.pem")}}
	if _, err := NewClient(context.Background(), badTLS, config.TemporalReconnectConfig{}, discardLogger()); err == nil || !strings.Contains(err.Error(), "CA bundle") {
		t.Errorf("bad CA: err = %v", err)
	}

	unreachable := config.TemporalConnectionConfig{Namespace: "ns", Host: "127.0.0.1:1"}
	if _, err := NewClient(context.Background(), unreachable, config.TemporalReconnectConfig{MaxAttempts: 1}, discardLogger()); err == nil || !strings.Contains(err.Error(), "dial") {
		t.Errorf("unreachable: err = %v", err)
	}
}

func TestConnectionsHealthValidateAndClose(t *testing.T) {
	_, addr := startFakeFrontend(t)
	cl, err := NewClient(context.Background(), config.TemporalConnectionConfig{Namespace: "default", Host: addr}, config.TemporalReconnectConfig{MaxAttempts: 1}, discardLogger())
	if err != nil {
		t.Fatal(err)
	}
	conns := Connections{"default": {Client: cl, Catalog: NewCatalog(nil)}}

	if got := conns.CheckHealth(context.Background()); len(got) != 1 || got["default"] != nil {
		t.Errorf("CheckHealth = %v, want default healthy", got)
	}

	apiSpec := &spec.Spec{Paths: map[string]spec.PathItem{"/x": {Post: &spec.Operation{Temporal: spec.TemporalSpec{Triggers: []spec.TemporalBinding{
		{Namespace: "default"}, {Namespace: "missing"},
	}}}}}}
	if err := ValidateNamespaces(apiSpec, conns); err == nil || !strings.Contains(err.Error(), `"missing"`) || strings.Contains(err.Error(), `"default"`) {
		t.Errorf("ValidateNamespaces err = %v, want only %q reported", err, "missing")
	}

	conns.Close()
	if _, err := cl.CheckHealth(context.Background(), nil); err == nil {
		t.Error("client still usable after Close")
	}
}

func TestCatalogTaskQueueFor(t *testing.T) {
	c := NewCatalog([]config.WorkflowDefinition{{Name: "A", TaskQueue: "qa"}, {Name: "NoQueue"}})
	if q, ok := c.TaskQueueFor("A"); !ok || q != "qa" {
		t.Errorf("TaskQueueFor(A) = %q, %v", q, ok)
	}
	for _, name := range []string{"NoQueue", "Unknown"} {
		if q, ok := c.TaskQueueFor(name); ok {
			t.Errorf("TaskQueueFor(%s) = %q, true; want false", name, q)
		}
	}
}

func TestParsePolicies(t *testing.T) {
	reuse := map[string]enumspb.WorkflowIdReusePolicy{
		"AllowDuplicateFailedOnly": enumspb.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE_FAILED_ONLY,
		"Bogus":                    enumspb.WORKFLOW_ID_REUSE_POLICY_UNSPECIFIED,
	}
	for name, want := range reuse {
		if got, _ := parseIDReusePolicy(name); got != want {
			t.Errorf("parseIDReusePolicy(%q) = %v, want %v", name, got, want)
		}
	}
	if _, ok := parseDuration("soon"); ok {
		t.Error(`parseDuration("soon") ok = true`)
	}
}

func TestNumericCoercions(t *testing.T) {
	if v, ok := toInt64(int64(4)); !ok || v != 4 {
		t.Errorf("toInt64(int64) = %v, %v", v, ok)
	}
	for _, in := range []any{float32(2), int(2), int64(2)} {
		if v, ok := toFloat64(in); !ok || v != 2 {
			t.Errorf("toFloat64(%T) = %v, %v", in, v, ok)
		}
	}
}
