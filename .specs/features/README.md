# `.specs/features/` — Gherkin specs + BDD automation status

Every `.feature` file here is valid Gherkin (checked with the real
`cucumber/gherkin` parser, not just by eye) and describes a feature's
expected behavior as concrete scenarios. See [`../README.md`](../README.md)
for the full index and how these relate to [`../adr/`](../adr/).

## BDD automation status

A subset of these files is wired up to [godog](https://github.com/cucumber/godog)
(Cucumber for Go) and actually **executed** against the real gateway code
via `go test ./bdd/...` — see the `bdd/` package at the repo root. The rest
are living documentation only, for now: real Gherkin, read by humans and
agents, but nothing runs their scenarios yet.

| Feature file | Automated? | Why / why not |
|---|---|---|
| `spec-driven-routing.feature` | ✅ `go test ./bdd/...` | Pure `spec.Load`/`spec.Routes`/`gateway.NewHandler` - no fake Temporal client needed. |
| `workflow-id-templating.feature` | ✅ `go test ./bdd/...` | Driven via `gateway.NewHandler` + an echo `Dispatcher` fake over real HTTP requests. |
| `request-validation.feature` | ✅ `go test ./bdd/...` | Same mechanism; a `recordingDispatcher` fake also proves dispatch is/isn't reached. |
| `multi-trigger-dispatch.feature` | ✅ `go test ./bdd/...` | A configurable fake `Dispatcher` (per-trigger success/fail, plus a channel-gated "slow" trigger to prove real concurrency) drives the full `returnStrategy` matrix. |
| `multi-namespace-temporal.feature` | ❌ not yet | Needs a real or faked `go.temporal.io/sdk/client.Client` to dial - no fake exists yet for that SDK interface. |
| `start-workflow-semantics.feature` | ❌ not yet | The started-vs-attached (`DescribeWorkflowExecution`/`RunID`) semantics need the same SDK client fake. |
| `other-temporal-actions.feature` | ❌ not yet | Same reason - `signalWorkflow`/`queryWorkflow`/etc. call the real SDK client directly. |
| `observability-tracing.feature` | ❌ not yet | Automatable with `go.opentelemetry.io/otel/sdk/trace/tracetest`'s in-memory exporter, just not done yet. |
| `response-and-server-hardening.feature` | ❌ not yet | The `Envelope`/status-shape scenarios are easy; the timeout/`SIGTERM`-shutdown scenarios need a live server + real signals/sockets (an integration test, not a unit-level one). |
| `environment-config.feature` | ❌ not yet | Every scenario except the two `--dry-run` ones is automatable today with existing exported functions (`envsubst.Expand`, `dotenv.Load`, `config.Load`, `spec.Load`); `--dry-run` itself needs a subprocess (`main`'s `run` is unexported and dials real Temporal connections). |
| `deployment-cicd.feature` | ❌ never (by nature) | Docker image / GitHub Actions behavior - not something `go test` can exercise. |

## Extending automation to another file

1. Add its path to `featurePaths` in `bdd/bdd_test.go`.
2. Run `go test ./bdd/... -v`; godog prints a ready-to-paste Go snippet for
   every undefined step it hits.
3. Implement each step against the gateway's **exported** API only
   (`gateway.NewHandler`, `spec.Load`, ...) — this suite is a black-box
   acceptance suite, not a white-box unit test, by design (see the
   package doc comment on `bdd/bdd_test.go`).
4. If a scenario genuinely needs something this suite can't provide yet
   (a fake SDK client, a live server, a subprocess), leave it out of
   `featurePaths` and update the table above rather than faking the
   assertion.

## Keeping `.feature` files valid Gherkin

A prose-style continuation line (e.g. starting with "So ..." or
"Regardless ...") is **not** valid Gherkin — only `Given`/`When`/`Then`/
`And`/`But`/`*` lines, comments (`#`), table rows, and doc strings are. A
step that needs more than one physical line must be written as a single
line, however long, or turned into a `#` comment explaining the *why*
rather than asserting more. Before trusting any edit to these files,
validate them with a real parser, not just by eye: `go test ./bdd/...`
fails loudly (`PARSE ERROR`, with line/column) on a syntax error in any
`.feature` file listed in `bdd/bdd_test.go`'s `featurePaths`. For a file
that isn't in that list yet, write a short one-off Go program calling
`gherkin.ParseGherkinDocument` (from `github.com/cucumber/gherkin/go/v42`,
already a dependency via godog) on it - the same check `go test` runs
internally for every file it's given.
