# temporal-gateway

A generic HTTP gateway in front of [Temporal](https://temporal.io), driven entirely by
an OpenAPI 3.0 specification. You describe your HTTP surface in YAML, annotate each
operation with an `x-temporal` extension saying what it should do against Temporal
(start a workflow, signal it, query it, ...), and the gateway generates the routes and
dispatches the calls - no handler code to write or maintain.

## Features

- **Spec-driven routing** - routes are generated from `api-spec.yaml`; adding an endpoint
  is a YAML change, not a code change.
- **One call, many workflows** - an operation's `x-temporal.triggers` is a list, so a
  single HTTP request can start/signal/query several workflows at once (dispatched
  concurrently), with `x-temporal.returnStrategy` controlling how their outcomes combine
  into one HTTP response.
- **Multi-namespace** - each `x-temporal.triggers` entry names which configured Temporal
  namespace (even a different cluster) it targets, so one gateway can front several.
- **Templated workflow IDs** - `workflowId: "order-{path.orderId}-{body.customerId}"`,
  with `{origin.field}` placeholders (`path`, `body`, `query`, `header`) plus a
  `{uuidv7}` generator.
- **Request validation** - the request body is validated against the operation's JSON
  Schema before anything is dispatched, with Laravel-style field-level error messages.
- **Accurate start semantics** - `startWorkflow` only reports `STARTED` when a new run
  was actually created; attaching to an existing run reports its real state
  (`WORKFLOW_RUNNING`, `WORKFLOW_COMPLETED`, ...) instead of a false positive.
- **Full `StartWorkflowOptions` coverage** - retry policy, memo, typed search
  attributes, cron/start-delay, priority, timeouts, ID reuse/conflict policies, and more,
  all settable from the spec.
- **OpenTelemetry tracing** - one span per request, exported via OTLP/gRPC and
  propagated into the workflow's Temporal headers so the trace continues inside the
  workflow; trace/span IDs are also attached to request logs.
- **Environment-aware config** - both `config.yml` and `api-spec.yaml` support
  `${VAR}` / `${VAR:-default}` shell-style expansion, with an optional `.env` file.

## Quick start

### Prerequisites

- Go 1.26+ (only needed to run from source; the Docker image is self-contained)
- A running [Temporal server](https://learn.temporal.io/getting_started/) reachable from
  the gateway (e.g. `temporal server start-dev` for local development)

### Run from source

```bash
go run . --config config.yml
```

By default the gateway looks for `config.yml` in the current directory, so if you're not
overriding anything you can just run:

```bash
go run .
```

The included `config.yml` and `api-spec.yaml` are a working example (an `OrderWorkflow` +
`NotificationWorkflow` pair) - point `TEMPORAL_HOST` at your Temporal server and try it:

```bash
curl -X POST localhost:8081/orders \
  -H 'Content-Type: application/json' \
  -d '{"orderId": "1234", "customerId": "cust-1"}'
```

### Run with Docker

```bash
docker build -f .docker/Dockerfile -t temporal-gateway .
docker run -p 8081:8081 \
  -e TEMPORAL_HOST=host.docker.internal:7233 \
  temporal-gateway
```

The image bakes in the example `config.yml`/`api-spec.yaml` as defaults. For a real
deployment, mount your own over them:

```bash
docker run -p 8081:8081 \
  -v $(pwd)/config.yml:/app/config.yml:ro \
  -v $(pwd)/api-spec.yaml:/app/api-spec.yaml:ro \
  -e TEMPORAL_HOST=temporal.internal:7233 \
  temporal-gateway
```

A pre-built image is published to GHCR on every GitHub Release (see
[CI/CD](#cicd) below): `ghcr.io/<owner>/temporal-gateway:<version>`.

## CLI flags

| Flag         | Default      | Description                                                          |
|--------------|--------------|------------------------------------------------------------------------|
| `--config`   | `config.yml` | Path to the gateway config file                                      |
| `--env`      | `.env`       | Path to a `.env` file to load into the process environment before config parsing (a missing file is not an error) |
| `--dry-run`  | `false`      | Load and validate the config, API spec, and Temporal connections/namespaces, then exit (0 on success, 1 on the first failure) without starting the HTTP server |

## Configuration (`config.yml`)

```yaml
server:
  host: "${GATEWAY_HOST:-0.0.0.0}"
  port: ${GATEWAY_PORT:-8081}

temporal:
  connections:
    - namespace: "${TEMPORAL_NAMESPACE:-default}"
      host: "${TEMPORAL_HOST:-localhost:7233}"
      tls:
        enabled: false
    - namespace: "${TEMPORAL_NOTIFICATIONS_NAMESPACE:-notifications}"
      host: "${TEMPORAL_NOTIFICATIONS_HOST:-localhost:7233}"
      tls:
        enabled: false

otel:
  enabled: ${OTEL_ENABLED:-true}
  serviceName: "${OTEL_SERVICE_NAME:-temporal-gateway}"
  endpoint: "${OTEL_EXPORTER_OTLP_ENDPOINT:-localhost:4317}"
  insecure: ${OTEL_EXPORTER_OTLP_INSECURE:-true}
  sampleRatio: ${OTEL_TRACES_SAMPLER_RATIO:-1.0}

apiSpec: "./api-spec.yaml"
```

- **`temporal.connections`** - one entry per Temporal namespace the gateway should dial.
  At least one is required, each `namespace` must be unique, and every
  `x-temporal.triggers` entry in the API spec must name a `namespace` present here
  (checked at startup, not at request time).
- **`otel`** - when `enabled` (the default), the gateway starts one span per request and
  exports it via OTLP/gRPC to `endpoint`, propagating trace context into the dispatched
  workflow's Temporal headers. The gRPC connection is non-blocking, so a missing
  collector at `endpoint` doesn't stop the gateway from starting - spans just fail to
  export.
- **`apiSpec`** - path to the OpenAPI spec, resolved relative to `config.yml`'s directory
  if not absolute. Also accepts a list of paths (`apiSpec: ["./base.yaml",
  "./overrides.yaml"]`), which are loaded and merged into a single spec, in order. A
  later file's operation (method + path) replaces an earlier file's definition of that
  same operation entirely; operations that only appear in one file are unaffected. This
  is useful for splitting a large spec across files, or layering an environment-specific
  overrides file on top of a shared base.
- Any scalar value in this file may use `${VAR}` or `${VAR:-default}`; a reference
  without a default fails config loading if the variable is unset.

## API specification (`api-spec.yaml`)

A standard OpenAPI 3.0 document, where each operation carries an `x-temporal` object: a
`triggers` list plus an optional `returnStrategy`:

```yaml
paths:
  /orders:
    post:
      operationId: createOrder
      requestBody:
        required: true
        content:
          application/json:
            schema:
              type: object
              required: [orderId, customerId]
              properties:
                orderId: { type: string }
                customerId: { type: string }
      x-temporal:
        returnStrategy: acceptPartial   # optional, this is the default - see below
        triggers:
          - action: startWorkflow
            namespace: default
            workflowType: OrderWorkflow
            workflowId: "order-{body.orderId}"
            taskQueue: orders-task-queue
            idReusePolicy: AllowDuplicate
            workflowIdConflictPolicy: TerminateExisting
```

### `returnStrategy`

Governs how a multi-trigger operation's overall HTTP response is derived from its
triggers' individual outcomes (a single-trigger operation always reports that trigger's
own outcome directly, regardless of this setting):

| Strategy                    | Behavior                                                                                          |
|------------------------------|----------------------------------------------------------------------------------------------------|
| `acceptPartial` (default)   | A genuine mix of outcomes (at least one trigger succeeded, at least one didn't) is reported as **207 Multi-Status**; a uniform outcome (all, or none) gets an ordinary single status code. |
| `allOrNothing`               | Anything short of every trigger succeeding - a partial mix or a total failure - is reported as a single **409 Conflict**. |

Every trigger is still dispatched and its individual result still appears in the
response's `results` array either way; `returnStrategy` only changes the top-level status
used to summarize them.

### Actions

| Action              | Required fields                    | What it does                                   |
|---------------------|-------------------------------------|-------------------------------------------------|
| `startWorkflow`     | `workflowType`, `taskQueue`         | Starts a workflow (see options below)          |
| `signalWorkflow`    | `signalName`                        | Sends a signal                                 |
| `queryWorkflow`     | `queryType`                         | Runs a query, returns its result as-is         |
| `cancelWorkflow`    | -                                    | Requests cancellation                          |
| `terminateWorkflow` | -                                    | Terminates immediately (body's `reason`, if any) |
| `getResult`         | -                                    | Blocks until the workflow completes, returns its result |

Every trigger, regardless of action, requires `namespace` and `workflowId`.

### `workflowId` templating

Any `x-temporal.triggers` string field may reference incoming request data via
`{origin.field}` placeholders:

- `{path.orderId}` - a path parameter
- `{body.customerId}` - a field from the JSON body
- `{query.filter}` - a query string parameter
- `{header.X-Request-Id}` - a request header
- `{uuidv7}` - generates a fresh UUIDv7 (no origin needed)

Example: `"order-{path.orderId}-{body.customerId}"`.

### `startWorkflow` options

A `startWorkflow` trigger may set any of the Temporal SDK's `StartWorkflowOptions`:

`workflowExecutionTimeout`, `workflowRunTimeout`, `workflowTaskTimeout` (durations like
`30s`/`5m`/`24h`), `workflowIdConflictPolicy` (`Fail`, `UseExisting`,
`TerminateExisting`), `idReusePolicy` (`AllowDuplicate`, `AllowDuplicateFailedOnly`,
`RejectDuplicate`, or `TerminateIfRunning`), `workflowExecutionErrorWhenAlreadyStarted`,
`retryPolicy` (`initialInterval`, `backoffCoefficient`, `maximumInterval`,
`maximumAttempts`, `nonRetryableErrorTypes`), `cronSchedule`, `startDelay` (mutually
exclusive with `cronSchedule`), `memo`, `enableEagerStart`, `staticSummary`,
`staticDetails`, `priority` (`priorityKey`, `fairnessKey`, `fairnessWeight`), and
`searchAttributes` - a list of typed attributes:

```yaml
searchAttributes:
  - name: CustomStringField
    type: string        # string | keyword | bool | int | float | time | keywordList
    value: widget
```

The whole spec is validated at startup - an invalid duration, unknown policy, type
mismatch, or a namespace with no matching `temporal.connections` entry fails the
gateway before it starts serving traffic.

## Request/response behavior

- The JSON body is validated against the operation's `requestBody` schema before
  dispatch; a failure returns **422** with a structured error naming every invalid
  field, not just the first.
- A single-trigger operation returns that trigger's result directly, with the HTTP
  status matching its outcome (e.g. `202` for a fresh `startWorkflow`, `409` if it
  attached to an already-running execution instead).
- A multi-trigger operation returns a batch result, each item with a `status` field
  identifying its own outcome, plus a top-level `status` and HTTP code summarizing all of
  them together, per `returnStrategy` (see [above](#returnstrategy)):
  `WORKFLOW_STARTED` (`200`/`202`) if every trigger succeeded; otherwise, under
  `acceptPartial`, `WORKFLOW_NOT_STARTED` (a single failure status) if none did, or
  `WORKFLOW_PARTIALLY_STARTED` (**207 Multi-Status**) for a genuine mix; under
  `allOrNothing`, `WORKFLOW_NOT_STARTED` (**409 Conflict**) for either case.

## CI/CD

- **`.docker/Dockerfile`** - multi-stage build producing a ~35 MB, non-root, distroless
  image.
- **`.github/workflows/docker-release.yml`** - on every published GitHub Release, builds
  `linux/amd64` + `linux/arm64` images and pushes them to
  `ghcr.io/<owner>/temporal-gateway`, tagged with the release version, `major.minor`, and
  `latest` (skipped for prereleases). Uses the built-in `GITHUB_TOKEN`, so no extra
  registry secrets are needed.

## Development

```bash
go build ./...
go vet ./...
gofmt -s -l .
go test -race ./...     # unit tests, plus the bdd/ BDD suite below
```

### BDD / acceptance suite

`bdd/` is a [godog](https://github.com/cucumber/godog) (Cucumber for Go)
suite that runs directly against the Gherkin specs in
[`.specs/features/`](.specs/features/) - the same `.feature` files that
document the project's behavior are, for a growing subset of them, also
executed as real acceptance tests:

```bash
go test ./bdd/...        # part of go test -race ./... too
go test ./bdd/... -v     # Cucumber-style pretty output per scenario
```

It drives the gateway only through its exported API
(`gateway.NewHandler`, `spec.Load`, ...) over real `net/http/httptest`
requests - a black-box suite, not a white-box unit test. See
[`.specs/features/README.md`](.specs/features/README.md) for exactly which
`.feature` files are wired up today and how to add more.

### Project layout

```
main.go              entrypoint: loads config/spec, wires everything, serves HTTP
internal/config      config.yml parsing
internal/spec        api-spec.yaml parsing + validation
internal/gateway     HTTP handler generation, request templating/validation
internal/temporal    Temporal client(s), namespace connection pool, dispatch
internal/validate    JSON Schema-lite request body validation
internal/response    response envelope + status types
internal/telemetry   OpenTelemetry setup
internal/envsubst    ${VAR}/${VAR:-default} expansion
internal/dotenv      .env file loading
bdd                  godog (BDD) suite executing .specs/features/*.feature
```

## License

[MIT](LICENSE)
