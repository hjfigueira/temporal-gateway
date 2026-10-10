# ADR-032: Startup and shutdown as ordered lifecycle modules in `internal/app`

**Status:** Accepted. Restructures the wiring described in [ADR-008](0008-fail-fast-validation-at-startup.md), [ADR-014](0014-http-server-hardening.md) and [ADR-026](0026-serve-http-before-temporal-connects.md) without changing their behavior.

**Related features:** [`response-and-server-hardening.feature`](../../features/response-and-server-hardening.feature), [`environment-config.feature`](../../features/environment-config.feature), [`health-probes.feature`](../../features/health-probes.feature)

## Context

`main.go` held `main`, a 130-line `run`, flag parsing, startup logging,
server construction, the health server and the graceful-shutdown loop.
Every new stage meant editing `run`'s control flow, its `defer` chain and
its hand-rolled "dial in the background, cancel serving on failure"
goroutine.

## Decision

- `main.go` contains only `main`: it sets up the logger and signal context,
  then passes an ordered list of modules to `app.Run`.
- `internal/app` holds the lifecycle engine and one `Module` per stage
  (`ParseFlags`, `LoadDotEnv`, `LoadConfig`, `Telemetry`, `LoadSpec`,
  `LogStartup`, `Temporal`, `DryRun`, `Health`, `API`). A module has
  three optional hooks:
  - **Init** runs in list order. It builds what the module owns and
    stores it on the shared `*app.State` for the modules after it.
    Returning `app.ErrDone` ends the program successfully (`-h`,
    `--dry-run`).
  - **Run** runs concurrently with every other module's Run. The first
    error cancels the others and is what `app.Run` returns. Errors that
    come after cancellation (a signal, or that first error) are ignored.
    A nil return stops nothing (Temporal finishing its dial).
  - **Stop** runs in reverse order for every module whose Init succeeded,
    each with its own `shutdownTimeout` context. It replaces `run`'s
    `defer`s.
- Data moves between modules through typed `State` fields, not a
  service locator. A module's private resources (a server, a telemetry
  flush function) stay in its constructor's closure.
- The domain packages (`config`, `spec`, `temporal`, `gateway`, ...)
  don't import `internal/app`. Only the composition root knows the
  lifecycle.

## Consequences

- A new stage is a new module plus one line in `main`. It doesn't touch
  the control flow of the other stages.
- Order is a contract: a module reads only what earlier modules set
  (e.g. `Health` needs `Temporal`'s `State.Connections`). Getting the
  order wrong panics with a nil pointer at startup, not in the middle of
  a request.
- `os.Exit` still runs exactly once, in `main`, after `app.Run` has
  unwound and every Stop hook has run (ADR-014).
