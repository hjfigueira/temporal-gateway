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
- **Request validation** - the OpenAPI spec is the validator: path, query and header
  parameters and the JSON body are checked against the operation (via
  [kin-openapi](https://github.com/getkin/kin-openapi)) before anything is dispatched,
  with every problem reported per field.
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
docker run -p 8081:8081 -p 8082:8082 \
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
| `--env`      | `.env`       | Path to a `.env` file (standard dotenv format, via [godotenv](https://github.com/joho/godotenv)) to load into the process environment before config parsing. A missing file is not an error, and variables already set in the environment win. Write values containing `$` in single quotes, e.g. `PASSWORD='Pa$SWORD1'`, or the `$NAME` part is expanded |
| `--dry-run`  | `false`      | Load and validate the config, API spec, and Temporal connections/namespaces (one dial attempt, no `temporal.reconnect` retries), then exit (0 on success, 1 on the first failure) without starting the HTTP server |

## Configuration (`config.yml`)

```yaml
server:
  host: "${GATEWAY_HOST:-0.0.0.0}"
  port: ${GATEWAY_PORT:-8081}
  maxBodyBytes: ${GATEWAY_MAX_BODY_BYTES:-1048576}
  requestTimeout: "${GATEWAY_REQUEST_TIMEOUT:-25s}"

temporal:
  reconnect:
    interval: "${TEMPORAL_RECONNECT_INTERVAL:-5s}"
    maxAttempts: ${TEMPORAL_RECONNECT_MAX_ATTEMPTS:-0}
  connections:
    - namespace: "${TEMPORAL_NAMESPACE:-default}"
      host: "${TEMPORAL_HOST:-localhost:7233}"
      tls:
        enabled: false
    - namespace: "${TEMPORAL_NOTIFICATIONS_NAMESPACE:-notifications}"
      host: "${TEMPORAL_NOTIFICATIONS_HOST:-localhost:7233}"
      tls:
        enabled: false

health:
  enabled: ${HEALTH_ENABLED:-true}
  host: "${HEALTH_HOST:-0.0.0.0}"
  port: ${HEALTH_PORT:-8082}

otel:
  enabled: ${OTEL_ENABLED:-true}
  serviceName: "${OTEL_SERVICE_NAME:-temporal-gateway}"
  endpoint: "${OTEL_EXPORTER_OTLP_ENDPOINT:-localhost:4317}"
  insecure: ${OTEL_EXPORTER_OTLP_INSECURE:-true}
  sampleRatio: ${OTEL_TRACES_SAMPLER_RATIO:-1.0}

apiSpec: "./api-spec.yaml"
```

- **`server.maxBodyBytes`** - largest accepted request body in bytes (default 1 MiB);
  a larger one gets **413**.
- **`server.requestTimeout`** - how long one request may spend dispatching to
  Temporal (default `25s`), e.g. a blocking `getResult`; exceeding it gets **504**.
  The server's write timeout is derived from it (`+5s`).
- **`temporal.connections`** - one entry per Temporal namespace the gateway should dial
  (all dialed concurrently). At least one is required, each `namespace` must be unique,
  and every `x-temporal.triggers` entry in the API spec must name a `namespace` present
  here (checked at startup, not at request time). An entry's optional `workflows` list
  (`name`, `taskQueue`, plus informational `signals`/`queries`) supplies the default
  task queue for a `startWorkflow` trigger that sets none; a trigger with neither
  fails startup. TLS options: `tls.enabled`, an
  optional mTLS keypair `tls.certPath` + `tls.keyPath` (both or neither), `tls.caPath`
  (a PEM bundle for private-CA clusters) and `tls.serverName`. `apiKey` authenticates
  with an API key (e.g. Temporal Cloud) and implies TLS; set it from an environment
  variable, not inline.
- **`temporal.reconnect`** - if a namespace's Temporal server can't be reached at
  startup, the gateway logs a warning and retries the dial every `interval` (a Go
  duration, default `5s`) instead of exiting. `maxAttempts` caps the number of dials
  per namespace (`0`, the default, retries until it connects or the process is
  stopped; if the attempts run out, the gateway exits). Dialing happens in the
  background: the HTTP server starts right away, and a namespace's routes answer
  **503** (`UNAVAILABLE`) until that namespace connects, while `/readyz` reports
  which namespaces are still pending. Once running, the Temporal SDK reconnects on
  its own if Temporal goes away; requests made while it's down get the same 503
  instead of crashing the gateway. `--dry-run` ignores this and makes a single,
  synchronous attempt.
- **`health`** - liveness and readiness probes, served on their own `host:port`
  (separate from `server`, so they can't collide with an API spec route). The probe
  server starts before Temporal is dialed. See [Health probes](#health-probes).
- **`otel`** - when `enabled` (the default), the gateway starts one span per request and
  exports it via OTLP/gRPC to `endpoint`, propagating trace context into the dispatched
  workflow's Temporal headers. The gRPC connection is non-blocking, so a missing
  collector at `endpoint` doesn't stop the gateway from starting - spans just fail to
  export.
- **`apiSpec`** - path to the OpenAPI spec, resolved relative to `config.yml`'s directory
  if not absolute. Also accepts a list of paths (`apiSpec: ["./base.yaml",
  "./overrides.yaml"]`), which are loaded and merged into a single spec, in order. A
  later file's operation (method + path) replaces an earlier file's definition of that
  same operation entirely; operations that only appear in one file are unaffected.
  Components merge per name, so a later file can `$ref` an earlier file's components
  (`$ref`s to other files are not supported). This
  is useful for splitting a large spec across files, or layering an environment-specific
  overrides file on top of a shared base.
- Any scalar value in this file may use `${VAR}` or `${VAR:-default}`; a reference
  without a default fails config loading if the variable is unset.

Every field above is described in
[`.specs/schemas/config.schema.json`](.specs/schemas/config.schema.json), and every
`x-temporal` option in [`.specs/schemas/x-temporal.schema.json`](.specs/schemas/x-temporal.schema.json).

## Health probes

| Endpoint      | Passes (200) when                                                     | Fails (503) when |
|---------------|-----------------------------------------------------------------------|------------------|
| `GET /livez`  | the process is up and answering HTTP                                  | never - no answer at all means the gateway is wedged |
| `GET /readyz` | every Temporal namespace is connected **and** answers a health check  | any namespace isn't connected yet (reported per namespace as `"not connected yet"`), any namespace fails its health check (2s timeout), or the gateway is shutting down |

`/readyz` returns which namespace failed and why, e.g.
`{"status":"not_ready","checks":{"default":"ok","notifications":"health check error: ..."}}`.

Liveness deliberately doesn't check Temporal: a Temporal outage should take the
gateway out of the load balancer (readiness), not get it restarted over and over
(liveness), since a restart can't fix Temporal. Example Kubernetes config:

```yaml
ports:
  - name: http
    containerPort: 8081
  - name: health
    containerPort: 8082
livenessProbe:
  httpGet: { path: /livez, port: health }
  periodSeconds: 10
readinessProbe:
  httpGet: { path: /readyz, port: health }
  periodSeconds: 5
  failureThreshold: 2
```

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
| `startWorkflow`     | `workflowType`, `taskQueue`¹        | Starts a workflow (see options below)          |
| `signalWorkflow`    | `signalName`                        | Sends a signal                                 |
| `queryWorkflow`     | `queryType`                         | Runs a query, returns its result as-is         |
| `cancelWorkflow`    | -                                    | Requests cancellation                          |
| `terminateWorkflow` | -                                    | Terminates immediately (body's `reason`, if any) |
| `getResult`         | -                                    | Blocks until the workflow completes, returns its result |

Every trigger, regardless of action, requires `namespace` and `workflowId`.

¹ Optional when the namespace's `workflows` entry in `config.yml` declares a
`taskQueue` for that `workflowType`.

### `workflowId` templating

A trigger's `workflowId` may reference incoming request data via `{origin.field}`
placeholders (other fields, such as `memo`, are sent literally):

- `{path.orderId}` - a path parameter
- `{body.customerId}` - a field from the JSON body
- `{query.filter}` - a query string parameter
- `{header.X-Request-Id}` - a request header
- `{body.customer.id}`, `{body.items[2].sku}` - nested body fields and array elements
- `{uuidv7}` - generates a fresh UUIDv7 (no origin needed)
- `{fingerprint(body)}`, `{fingerprint(body.items[2])}` - a stable 16-hex-char hash
  (SHA-256 of the canonical JSON) of the whole body or any referenced value; the same
  content always gives the same ID, whatever its key order, so it works well as a
  deduplication key. Fingerprint a stable part of the body if it carries volatile
  fields like timestamps.

Example: `"order-{path.orderId}-{body.customerId}"`.

Placeholders are checked at startup: an unknown origin, a missing origin, or a
`{path.X}` that isn't one of the route's path parameters fails spec loading. A request
that doesn't supply a placeholder's value (e.g. no `customerId` in the body) gets
**422** and nothing is dispatched.

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

- Every request is validated against its OpenAPI operation before dispatch: path,
  query and header parameters, and the `requestBody` (its `required` flag, its
  `Content-Type`, and its schema). A failure returns **422** `VALIDATION_FAILED` with
  a `fields` map naming every problem, not just the first (e.g. `"query.limit"`,
  `"items[1].sku"`, or `"_body"` for the body as a whole). A body sent without a
  `Content-Type` is treated as JSON. The spec's `security` schemes are not enforced.
- A route whose Temporal namespace isn't connected (yet), or a Temporal that reports
  itself unavailable, returns **503** (`UNAVAILABLE`).
- A body over `server.maxBodyBytes` returns **413**; a dispatch exceeding
  `server.requestTimeout` returns **504** (`TIMEOUT`).
- Temporal errors are reported with a fixed message per class (not found, already
  started, invalid argument, permission denied, timed out, failed); the raw error is
  only logged.
- A single-trigger operation returns that trigger's result directly, with the HTTP
  status matching its outcome (e.g. `202` for a fresh `startWorkflow`, `409` if it
  attached to an already-running execution instead).
- A multi-trigger operation returns a batch result, each item with a `status` field
  identifying its own outcome, plus a top-level `status` and HTTP code summarizing all of
  them together, per `returnStrategy` (see [above](#returnstrategy)):
  `WORKFLOW_STARTED` (`200`/`202`) if every trigger succeeded; otherwise, under
  `acceptPartial`, `WORKFLOW_NOT_STARTED` (a single failure status) if none did, or
  `WORKFLOW_PARTIALLY_STARTED` (**207 Multi-Status**) for a genuine mix; under
  `allOrNothing`, `WORKFLOW_NOT_STARTED` (**409 Conflict**) for either case. When
  not every trigger is a `startWorkflow` (a signal fan-out, say), the same three
  outcomes are named `BATCH_SUCCEEDED` / `BATCH_FAILED` /
  `BATCH_PARTIALLY_SUCCEEDED` instead.

## CI/CD

- **`.github/workflows/ci.yml`** - on every push and pull request to `main`, fails on
  unformatted code (`gofmt -s -l`), then runs `go vet ./...` and `go test -race ./...`.
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
go test -race ./...
```

### Project layout

```
main.go              entrypoint: only main(); lists the internal/app modules in order
internal/app         lifecycle engine (Init/Run/Stop) + one module per startup stage
internal/config      config.yml parsing
  envsubst/          ${VAR}/${VAR:-default} expansion (config.yml + api-spec.yaml)
internal/spec        api-spec.yaml parsing + validation (kin-openapi doc + x-temporal)
internal/gateway     HTTP handler generation, request validation (kin-openapi), dispatch
  health/            /livez + /readyz probe server (own port, ADR-019)
internal/templating  workflowId placeholders: parsing, rendering, fingerprint
internal/temporal    Temporal client(s), namespace connection pool, dispatch
internal/response    response envelope + status types (shared by gateway and temporal)
internal/telemetry   OpenTelemetry setup
```

## License

[MIT](LICENSE)
