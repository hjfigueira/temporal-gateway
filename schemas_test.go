package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"gopkg.in/yaml.v3"

	"temporal-gateway/internal/config"
	"temporal-gateway/internal/config/envsubst"
	"temporal-gateway/internal/spec"
)

// The schemas in .specs/schemas document config.yml and x-temporal. These
// tests keep them honest: the repo's own files must pass, and for every rule
// the code enforces, the schema and the loader must agree on what's valid.

func compileSchema(t *testing.T, name string) *jsonschema.Schema {
	t.Helper()
	sch, err := jsonschema.NewCompiler().Compile(filepath.Join(".specs", "schemas", name))
	if err != nil {
		t.Fatalf("compile %s: %v", name, err)
	}
	return sch
}

// asJSON converts a YAML-decoded value into the JSON value model the
// validator expects.
func asJSON(t *testing.T, v any) any {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	out, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func decodeYAML(t *testing.T, data []byte) any {
	t.Helper()
	var v any
	if err := yaml.Unmarshal(data, &v); err != nil {
		t.Fatal(err)
	}
	return asJSON(t, v)
}

func TestSchemasAcceptRepoFiles(t *testing.T) {
	configSchema := compileSchema(t, "config.schema.json")
	raw, err := os.ReadFile("config.yml")
	if err != nil {
		t.Fatal(err)
	}
	if err := configSchema.Validate(decodeYAML(t, raw)); err != nil {
		t.Errorf("config.yml (unexpanded): %v", err)
	}
	expanded, err := envsubst.Expand(raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := configSchema.Validate(decodeYAML(t, expanded)); err != nil {
		t.Errorf("config.yml (expanded): %v", err)
	}

	xTemporal := compileSchema(t, "x-temporal.schema.json")
	for _, file := range []string{"api-spec.yaml", "tests/api-spec-full.yaml", "tests/api-spec-full-2.yaml"} {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		var doc struct {
			Paths map[string]map[string]any `yaml:"paths"`
		}
		if err := yaml.Unmarshal(data, &doc); err != nil {
			t.Fatal(err)
		}
		for path, item := range doc.Paths {
			for method, op := range item {
				// Path-level keys such as parameters aren't operations.
				op, _ := op.(map[string]any)
				if op["x-temporal"] == nil {
					continue
				}
				if err := xTemporal.Validate(asJSON(t, op["x-temporal"])); err != nil {
					t.Errorf("%s %s %s: %v", file, method, path, err)
				}
			}
		}
	}
}

func TestXTemporalSchemaAgreesWithSpecLoad(t *testing.T) {
	sch := compileSchema(t, "x-temporal.schema.json")
	const start = "action: startWorkflow, namespace: default, workflowId: w, workflowType: W"
	tests := []struct {
		name, xTemporal string
		valid           bool
	}{
		{"start with taskQueue", "triggers: [{" + start + ", taskQueue: q}]", true},
		{"start without taskQueue (catalog's job)", "triggers: [{" + start + "}]", true},
		{"every option", `{returnStrategy: allOrNothing, triggers: [{` + start + `, idReusePolicy: AllowDuplicateFailedOnly,
			workflowIdConflictPolicy: UseExisting, workflowRunTimeout: 1h30m, startDelay: 500ms, memo: {a: 1},
			retryPolicy: {initialInterval: 1s, backoffCoefficient: 2, maximumAttempts: 3},
			priority: {priorityKey: 1, fairnessKey: k, fairnessWeight: 1.5},
			searchAttributes: [{name: A, type: int, value: 1}, {name: B, type: float, value: 2},
				{name: C, type: time, value: "2030-01-01T00:00:00Z"}, {name: D, type: keywordList, value: [x]}]}]}`, true},
		{"signal and query", "triggers: [{action: signalWorkflow, namespace: d, workflowId: w, signalName: s}, {action: queryWorkflow, namespace: d, workflowId: w, queryType: q}]", true},
		{"cancel, terminate, getResult", "triggers: [{action: cancelWorkflow, namespace: d, workflowId: w}, {action: terminateWorkflow, namespace: d, workflowId: w}, {action: getResult, namespace: d, workflowId: w}]", true},
		{"no triggers", "returnStrategy: acceptPartial", false},
		{"empty triggers", "triggers: []", false},
		{"unknown returnStrategy", "{returnStrategy: some, triggers: [{action: getResult, namespace: d, workflowId: w}]}", false},
		{"unknown action", "triggers: [{action: frobnicate, namespace: d, workflowId: w}]", false},
		{"missing namespace", "triggers: [{action: getResult, workflowId: w}]", false},
		{"missing workflowId", "triggers: [{action: getResult, namespace: d}]", false},
		{"start without workflowType", "triggers: [{action: startWorkflow, namespace: d, workflowId: w, taskQueue: q}]", false},
		{"signal without signalName", "triggers: [{action: signalWorkflow, namespace: d, workflowId: w}]", false},
		{"query without queryType", "triggers: [{action: queryWorkflow, namespace: d, workflowId: w}]", false},
		{"unknown idReusePolicy", "triggers: [{" + start + ", idReusePolicy: Bogus}]", false},
		{"unknown conflict policy", "triggers: [{" + start + ", workflowIdConflictPolicy: Bogus}]", false},
		{"TerminateIfRunning with conflict policy", "triggers: [{" + start + ", idReusePolicy: TerminateIfRunning, workflowIdConflictPolicy: Fail}]", false},
		{"cron with startDelay", `triggers: [{` + start + `, cronSchedule: "* * * * *", startDelay: 5s}]`, false},
		{"bad duration", `triggers: [{` + start + `, workflowTaskTimeout: "1 day"}]`, false},
		{"bad retry duration", `triggers: [{` + start + `, retryPolicy: {maximumInterval: soon}}]`, false},
		{"search attribute type mismatch", "triggers: [{" + start + ", searchAttributes: [{name: A, type: int, value: x}]}]", false},
		{"search attribute bad time", "triggers: [{" + start + ", searchAttributes: [{name: A, type: time, value: tomorrow}]}]", false},
		{"search attribute unknown type", "triggers: [{" + start + ", searchAttributes: [{name: A, type: uuid, value: x}]}]", false},
		{"search attribute without name", "triggers: [{" + start + ", searchAttributes: [{type: bool, value: true}]}]", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var xTemporal any
			if err := yaml.Unmarshal([]byte(tt.xTemporal), &xTemporal); err != nil {
				t.Fatal(err)
			}
			schemaErr := sch.Validate(asJSON(t, xTemporal))

			doc := map[string]any{
				"openapi": "3.0.3",
				"info":    map[string]any{"title": "T", "version": "1"},
				"paths":   map[string]any{"/x": map[string]any{"post": map[string]any{"operationId": "x", "x-temporal": xTemporal}}},
			}
			data, err := yaml.Marshal(doc)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "api-spec.yaml")
			if err := os.WriteFile(path, data, 0o644); err != nil {
				t.Fatal(err)
			}
			_, loadErr := spec.Load(path)

			if (schemaErr == nil) != tt.valid || (loadErr == nil) != tt.valid {
				t.Errorf("want valid=%v; schema: %v; spec.Load: %v", tt.valid, schemaErr, loadErr)
			}
		})
	}
}

func TestConfigSchemaAgreesWithConfigLoad(t *testing.T) {
	sch := compileSchema(t, "config.schema.json")
	const conns = "temporal: {connections: [{namespace: default, host: \"localhost:7233\"}]}\n"
	const spec = "apiSpec: ./api-spec.yaml\n"
	tests := []struct {
		name, yaml string
		valid      bool
	}{
		{"minimal", conns + spec, true},
		{"every section", spec + `server: {host: 0.0.0.0, port: 8081, maxBodyBytes: 1024, requestTimeout: 2m}
temporal:
  reconnect: {interval: 5s, maxAttempts: 3}
  connections:
    - namespace: default
      host: "localhost:7233"
      apiKey: secret
      tls: {enabled: true, certPath: c.pem, keyPath: k.pem, caPath: ca.pem, serverName: t}
      workflows: [{name: W, taskQueue: q, signals: [s], queries: [q]}]
health: {enabled: true, host: 0.0.0.0, port: 8082}
otel: {enabled: true, serviceName: g, endpoint: "localhost:4317", insecure: true, sampleRatio: 0.5}
auth: {type: apiKey, apiKey: {header: X-Api-Key, keys: [k]}}
middlewares: [{name: cors, enabled: true, config: {origins: ["*"]}}]
`, true},
		{"apiSpec list", conns + "apiSpec: [a.yaml, b.yaml]\n", true},
		{"no connections", "temporal: {connections: []}\n" + spec, false},
		{"no temporal section", spec, false},
		{"connection without namespace", "temporal: {connections: [{host: h}]}\n" + spec, false},
		{"certPath without keyPath", "temporal: {connections: [{namespace: d, tls: {certPath: c.pem}}]}\n" + spec, false},
		{"no apiSpec", conns, false},
		{"empty apiSpec", conns + "apiSpec: \"\"\n", false},
		{"negative maxBodyBytes", conns + spec + "server: {maxBodyBytes: -1}\n", false},
		{"zero requestTimeout", conns + spec + "server: {requestTimeout: 0s}\n", false},
		{"bad requestTimeout", conns + spec + "server: {requestTimeout: soon}\n", false},
		{"negative reconnect attempts", "temporal: {reconnect: {maxAttempts: -1}, connections: [{namespace: d}]}\n" + spec, false},
		{"zero reconnect interval", "temporal: {reconnect: {interval: 0s}, connections: [{namespace: d}]}\n" + spec, false},
		{"health enabled without port", conns + spec + "health: {enabled: true}\n", false},
		{"health port out of range", conns + spec + "health: {enabled: true, port: 70000}\n", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			schemaErr := sch.Validate(decodeYAML(t, []byte(tt.yaml)))

			path := filepath.Join(t.TempDir(), "config.yml")
			if err := os.WriteFile(path, []byte(tt.yaml), 0o644); err != nil {
				t.Fatal(err)
			}
			_, loadErr := config.Load(path)

			if (schemaErr == nil) != tt.valid || (loadErr == nil) != tt.valid {
				t.Errorf("want valid=%v; schema: %v; config.Load: %v", tt.valid, schemaErr, loadErr)
			}
		})
	}
}

func TestSchemasRejectUnknownKeys(t *testing.T) {
	// Stricter than the loaders on purpose: the gateway ignores unknown
	// keys, the schemas flag them as likely typos.
	xTemporal := compileSchema(t, "x-temporal.schema.json")
	err := xTemporal.Validate(decodeYAML(t, []byte("triggers: [{action: getResult, namespace: d, workflowID: w}]")))
	if err == nil || !strings.Contains(err.Error(), "workflowID") {
		t.Errorf("x-temporal with workflowID typo: %v", err)
	}
	configSchema := compileSchema(t, "config.schema.json")
	if err := configSchema.Validate(decodeYAML(t, []byte("apiSpec: a.yaml\ntemporal: {connections: [{namespace: d}]}\nserevr: {}\n"))); err == nil {
		t.Error("config with serevr typo: want an error")
	}
}
