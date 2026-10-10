# ADR-022: Per-request body limit, dispatch deadline, and sanitized errors

**Status:** Accepted

**Related features:** [`response-and-server-hardening.feature`](../../features/response-and-server-hardening.feature)

## Context

[ADR-014](0014-http-server-hardening.md) bounded *connection* time, but not
what a single request can cost:

- The JSON body was decoded with no size cap, so one request could
  exhaust memory under the container's soft cap (ADR-016).
- `getResult` blocks until the workflow completes, which can take days.
  `WriteTimeout` doesn't cancel the request context, so the handler and its
  Temporal long-poll outlived the response the client had already lost.
- Raw Temporal error strings, which can name namespaces, hosts and server
  internals, were returned to callers.

## Decision

- `server.maxBodyBytes` (default 1 MiB) wraps the body in
  `http.MaxBytesReader`. A larger body gets **413** `PAYLOAD_TOO_LARGE`.
- `server.requestTimeout` (default `25s`) bounds the Temporal dispatch with
  `context.WithTimeout`. Exceeding it gets **504** `TIMEOUT`. The server's
  `WriteTimeout` is derived as `requestTimeout + 5s`, so the handler's own
  deadline always answers first. A long `getResult` needs one setting.
- Dispatch errors map to fixed client-facing messages per class (not
  found, already started, invalid argument, permission denied, timed out,
  failed). The raw error is only logged. JSON-decode errors still echo the
  parser message, since it only describes the caller's own input.

## Consequences

- Operations that legitimately wait longer (a slow `getResult`) need
  `server.requestTimeout` raised. It applies to every route; there is no
  per-operation override yet.
- Callers debugging a 502 must correlate with the gateway logs (`trace_id`
  when tracing is on) instead of reading the Temporal error in the response.
