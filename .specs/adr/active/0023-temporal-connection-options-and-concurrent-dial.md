# ADR-023: Concurrent namespace dial; CA bundle, server name, and API key

**Superseded in part by [ADR-026](0026-serve-http-before-temporal-connects.md)**: `NewConnections` only builds the namespace table and `Connect` dials in the background; serving no longer waits for any namespace.

**Status:** Accepted

**Related features:** [`multi-namespace-temporal.feature`](../../features/multi-namespace-temporal.feature)

## Context

Namespaces were dialed one after another. With [ADR-018](0018-retry-temporal-dial-at-startup.md)'s
retry, one slow or unreachable namespace delayed every later one, and only
the first failure was reported. TLS supported only a client keypair. There
was no private CA, no server-name override, and no API key, so private-CA
clusters and Temporal Cloud API-key auth couldn't connect.

## Decision

- `NewConnections` dials every namespace concurrently. Startup waits for
  the slowest one, not the sum of all. Every failure is reported together
  (`errors.Join`), and the clients that did connect are still returned so
  they can be closed. Serving still waits for **all** namespaces
  (ADR-018/019 unchanged).
- `tls.caPath` (a PEM bundle replacing the system roots) and
  `tls.serverName` are added. `tls.certPath`/`tls.keyPath` must be set
  together, which is checked at config load.
- A per-connection `apiKey` sets `client.NewAPIKeyStaticCredentials`. The
  SDK turns on TLS by itself for API keys. The key is meant to come from
  `${VAR}` and is never logged.

## Consequences

- A bad CA file or keypair fails startup immediately, as a configuration
  error that isn't retried (ADR-018).
- `envsubst` also expands `${…}` inside YAML comments, so examples in
  `config.yml` must not contain an unset `${VAR}` reference.
