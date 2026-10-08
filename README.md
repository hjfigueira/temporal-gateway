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
- **Pluggable dispatch drivers** - `x-temporal.driver` picks how an operation's triggers
  reach Temporal: `direct` (dispatched straight to Temporal, the default) or `nexus` (a
  single `CascadeEvent` workflow the gateway itself hosts, fanning them out over Nexus) -
  see [`driver`](#driver) below.
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
  (checked at startup, not at request time). `nexusEndpoint`, `nexusDispatchTaskQueue`,
  and `nexusDispatchExternal` are only needed by namespaces a `driver: nexus` operation
  touches - see [`driver`](#driver).
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

### `driver`

Governs *how* an operation's `x-temporal.triggers` actually reach Temporal. `DirectDriver`
and `NexusDriver` implement a common `Driver` interface (`internal/gateway`), so the two
are fully interchangeable per operation - switch one for the other by editing `driver`
alone, no code change.

| Driver              | Behavior                                                                                     |
|----------------------|-----------------------------------------------------------------------------------------------|
| `direct` (default)  | Every trigger is dispatched straight to Temporal, concurrently - exactly as described above. |
| `nexus`              | The gateway starts a single **CascadeEvent** workflow instead, passing `triggers` as its input; that workflow fans them out to their target namespaces over [Nexus](https://docs.temporal.io/nexus). |

`nexus` requires a sibling `config` object naming the CascadeEvent workflow to start:

```yaml
x-temporal:
  driver: nexus
  config:
    namespace: cascade                          # a temporal.connections entry
    taskQueue: cascade-task-queue
    workflowType: CascadeEvent                  # optional, this is the default
    workflowId: "cascade-order-{body.orderId}"  # supports the same templating as triggers
  triggers:
    - action: startWorkflow
      namespace: default
      workflowType: OrderWorkflow
      workflowId: "order-{body.orderId}"
      taskQueue: orders-task-queue
    - action: startWorkflow
      namespace: notifications
      workflowType: NotificationWorkflow
      workflowId: "order-{body.orderId}-notification"
      taskQueue: notifications-task-queue
```

What actually happens for a `nexus` request:

1. The gateway renders each trigger's `workflowId` against the request (same templating as
   `direct`), then starts the CascadeEvent workflow named by `config`, passing the
   rendered triggers - plus the request body - as its input. **This is the only Temporal
   call the HTTP request makes**; the response reports only this workflow's own start
   outcome (e.g. `202`/`STARTED`), not each trigger's, since those happen asynchronously,
   inside the workflow.
2. CascadeEvent (a workflow the gateway itself hosts - see below) fans each trigger out
   concurrently over Nexus to the namespace it targets, invoking a `Dispatch` Nexus
   operation there. That operation hands off to the exact same dispatch logic the
   `direct` driver uses in-process, so a trigger behaves identically either way - only how
   the call arrives differs.

**The gateway itself runs the Temporal workers this requires** - it's not a pure
stateless HTTP↔Temporal translator once any operation uses `nexus`. At startup it
inspects the loaded spec and, for every operation using `driver: nexus`, starts:

- One worker per distinct `config.namespace`/`config.taskQueue`, hosting the
  `CascadeEvent` workflow.
- One worker per namespace targeted by a `nexus`-driver trigger, hosting the `Dispatch`
  Nexus service that lets a CascadeEvent workflow reach it.

This also means every namespace a `nexus`-driver trigger targets needs two things set up
beyond what `direct` requires:

- **`temporal.connections[].nexusEndpoint`** (config.yml) - the name of a [Nexus
  endpoint](https://docs.temporal.io/nexus/endpoint) reaching that namespace. Nexus
  endpoints are a server-side resource, provisioned separately (e.g. `temporal operator
  nexus endpoint create --name gateway-notifications --target-namespace notifications
  --target-task-queue temporal-gateway-nexus-dispatch`) - the gateway only references
  them by name, and validates at startup that every namespace needing one has it
  configured (fails fast, same as an unconfigured `namespace`).
- **`temporal.connections[].nexusDispatchTaskQueue`** (optional) - the task queue that
  namespace's `Dispatch` service worker polls; defaults to
  `temporal-gateway-nexus-dispatch`. Whatever's actually in effect must match the target
  task queue the Nexus endpoint above was provisioned with.

A gateway spec that never uses `driver: nexus` never starts a worker or polls a task
queue at all - this is entirely opt-in per operation.

### Letting the called service own dispatch: `nexusDispatchExternal`

By default the gateway hosts the `Dispatch` Nexus service for *every* namespace a
`nexus`-driver trigger targets, using its own generic dispatch logic - fine as long as
"start whatever CascadeEvent says to start" is all a namespace needs. It often isn't:
CascadeEvent fans a trigger's payload out to every namespace configured for it,
indiscriminately, but the namespace on the receiving end may only care about *some* of
what shows up. Deciding that is business logic that belongs to whoever owns the workflow
being started, not to the gateway's generic dispatcher - so a namespace can opt out and
host that decision (and the workflow) itself:

```yaml
temporal:
  connections:
    - namespace: default
      host: localhost:7233
      nexusEndpoint: gateway-default
      nexusDispatchExternal: true   # some other service hosts Dispatch for this namespace
```

With `nexusDispatchExternal: true`, the gateway skips building a `Dispatch` worker for
that namespace entirely - `gateway-default`'s Nexus endpoint must then be provisioned to
target whatever task queue the external service actually polls, not the gateway's own
`temporal-gateway-nexus-dispatch`. See **`cmd/order-service`** for a complete worked
example: it's a standalone sample application - not part of the gateway - that owns
`OrderWorkflow` (the workflow `createOrderViaCascade`'s first trigger targets) and hosts
its own `Dispatch` Nexus service for the `default` namespace, honoring the exact same
`DispatchServiceName`/`DispatchOperationName` contract
(`internal/temporal.NewDispatchService`) the gateway's generic version does - so
CascadeEvent calls it exactly the same way and can't tell the difference. Before starting
anything, its operation handler validates the cascaded payload (`validateOrderPayload` in
`cmd/order-service/main.go` - rejects an order with no `items`, as a stand-in for
whatever real relevance check a production service would apply) and only calls
`ExecuteWorkflow` once that passes; an irrelevant payload is rejected right there, so no
workflow run is ever created for it. Run it alongside the gateway and a Temporal server:

```bash
go run ./cmd/order-service
```

It reads the same `TEMPORAL_NAMESPACE`/`TEMPORAL_HOST`/`ORDERS_TASK_QUEUE` env vars (and
`.env` file) config.yml/api-spec.yaml already use, plus its own
`ORDER_SERVICE_NEXUS_TASK_QUEUE` (defaults to `order-service-nexus-dispatch`) - that's
the task queue `gateway-default`'s Nexus endpoint needs to target:

```bash
temporal operator nexus endpoint create \
  --name gateway-default \
  --target-namespace default \
  --target-task-queue order-service-nexus-dispatch
```

Note this validation only runs for the cascaded path (`POST /orders/{orderId}/cascade`);
plain `POST /orders` (the `direct` driver) talks to Temporal directly and never goes
through Nexus, so it bypasses `order-service`'s validation entirely - the two triggers
just happen to start the same `OrderWorkflow`.

#### The same pattern in another language: `notification-service` (PHP)

`order-service` hosts its `Dispatch` operation on a Temporal *worker* - a Nexus endpoint
whose target is a namespace/task queue that a worker polls, same as any other Temporal
task. That's the norm for SDKs with Nexus support (Go, Java, Python, TypeScript, .NET).
The PHP SDK isn't one of them: `sdk-php` has no equivalent of
`worker.RegisterNexusService` at all
([temporalio/sdk-php#580](https://github.com/temporalio/sdk-php/issues/580), open,
unimplemented). **`notification-service`** - a second standalone sample application, this
one owning `NotificationWorkflow` (`createOrderViaCascade`'s second trigger) - shows the
workaround: Temporal Nexus endpoints support a second kind of target besides a worker,
an **external URL**, which receives forwarded Nexus requests as plain HTTP - no SDK
worker abstraction involved on that side at all. `notification-service`'s
`http-worker.php` hand-implements just enough of the Nexus-over-HTTP protocol (a
[RoadRunner](https://roadrunner.dev) HTTP worker, no framework) to receive that request.

Unlike `order-service`, where the Nexus operation handler decides relevance inline
(`validateOrderPayload`, a plain Go function call), `notification-service` decides it
through a second workflow: **`BusinessRulesWorkflow`**
(`notification-service/src/BusinessRulesWorkflow.php`) is a long-running, singleton
workflow (one standing instance per namespace, fixed ID
`BusinessRulesWorkflow::WORKFLOW_ID`) that exposes relevance as a
[Query](https://docs.temporal.io/develop/php/message-passing#queries) - `isEventRelevant`
- genuine, durable Temporal PHP SDK workflow code, executed by a real worker
(`worker.php`) inside the `notifications` namespace, the same way any other PHP SDK user
would expose read-only state to the outside world. `http-worker.php`'s `dispatch()`
lazily starts that standing instance (a no-op once it's already running - see
`queryEventRelevance`) and queries it *before* starting `NotificationWorkflow` at all: an
irrelevant event is rejected right there, so - same guarantee `order-service`'s Go gives,
just reached via a Query instead of an inline function call - no `NotificationWorkflow`
run is ever created for it. `NotificationWorkflow` itself carries no relevance logic
anymore; by the time it starts, the event has already been confirmed relevant.

`BusinessRulesWorkflow` keeps itself alive with `Workflow::awaitWithTimeout` +
`Workflow::continueAsNew` on a 30-day cycle - standard practice for a long-running/entity
workflow that would otherwise accumulate unbounded history, and unlike
`isEventRelevant` (a pure function of its input today, same rule
`order-service`'s `validateOrderPayload` applies in Go), it's the seam a real deployment
would use to give the workflow actual state - fetched via Activity, updated via Signal -
for `isEventRelevant` to read.

One pitfall worth calling out, since it's easy to hit and silent when you do:
`Temporal\Client\WorkflowClient::create()` defaults to the `"default"` namespace unless
given a `ClientOptions::withNamespace(...)` explicitly - found by hand when
`NotificationWorkflow` runs kept showing up under `default` instead of `notifications`.
`http-worker.php` sets it from `TEMPORAL_NOTIFICATIONS_NAMESPACE`.

The wire format `http-worker.php` implements was verified by hand against a real
Temporal server (not just read off a spec): a synchronous Nexus operation arrives as
`POST {endpoint's target URL}/{service}/{operation}` (for us,
`/TemporalGatewayDispatch/Dispatch`), body = JSON-encoded
`internal/spec.DispatchInput`; a synchronous success is `200` + JSON
`internal/spec.DispatchOutput`; a non-retryable rejection is `400` with a body carrying
`metadata.type: "nexus.HandlerError"` and `details.type: "BAD_REQUEST"` - anything else
(a plain error body, or any `5xx`) is treated as retryable and gets retried indefinitely,
same pitfall `order-service`'s Go handler has to avoid (see its
`HandlerErrorTypeBadRequest` comment).

Run it (needs `ext-grpc` - the Temporal PHP SDK's Client component hard-requires the
native extension at runtime even though `composer install` alone succeeds without it;
the worker side doesn't need it, only the client-side `WorkflowClient::create` call in
`http-worker.php` does). The `Dockerfile` builds on
[`beabys/php-grpc`](https://hub.docker.com/r/beabys/php-grpc), which ships `ext-grpc`
prebuilt - compiling it from source via `pecl` works too but takes a long time:

```bash
cd notification-service
docker build -t notification-service . && docker run --rm -p 8083:8083 \
  --add-host=host.docker.internal:host-gateway notification-service
```

(`--add-host` is only needed on Linux - Docker Desktop resolves `host.docker.internal` automatically.
If your Temporal server is itself in Docker, join its network instead, e.g. `--network
temporal-network -e TEMPORAL_HOST=temporal:7233`.)

or, if your PHP already has `ext-grpc` installed, without Docker:

```bash
cd notification-service
composer install
composer require --dev spiral/roadrunner-cli && vendor/bin/rr get
vendor/bin/rr serve
```

Its Nexus endpoint is provisioned with `--target-url` instead of
`--target-namespace`/`--target-task-queue`:

```bash
temporal operator nexus endpoint create \
  --name gateway-notifications \
  --target-url http://<host-reachable-from-the-temporal-server>:8083
```

(`--target-url` is marked **experimental** by Temporal itself - see
[the self-hosted Nexus guide](https://docs.temporal.io/production-deployment/self-hosted-guide/nexus).)
`config.yml`'s `notifications` connection still sets `nexusDispatchExternal: true`, for
exactly the same reason `default`'s does - the gateway shouldn't also try to host a
`Dispatch` worker nothing will ever call.

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
go test -race ./...
```

### Project layout

```
main.go                entrypoint: loads config/spec, wires everything, serves HTTP
cmd/order-service       sample app (Go): owns OrderWorkflow, worker-target Nexus endpoint
notification-service   sample app (PHP/RoadRunner): owns NotificationWorkflow, external-URL Nexus endpoint
internal/config        config.yml parsing
internal/spec          api-spec.yaml parsing + validation
internal/gateway       HTTP handler generation, request templating/validation, driver selection
internal/temporal      Temporal client(s), namespace connection pool, dispatch, CascadeEvent + nexus workers
internal/validate      JSON Schema-lite request body validation
internal/response      response envelope + status types
internal/telemetry     OpenTelemetry setup
internal/envsubst      ${VAR}/${VAR:-default} expansion
internal/dotenv        .env file loading
```

## License

[MIT](LICENSE)
