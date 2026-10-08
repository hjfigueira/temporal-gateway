# ADR-011: `${VAR}` / `${VAR:-default}` expansion over raw config bytes, before YAML parsing

**Status:** Accepted

**Related features:** [`environment-config.feature`](../features/environment-config.feature)

## Context

`config.yml` and `api-spec.yaml` need environment-specific values (hosts,
namespaces, task queue names) without baking them into the checked-in
files or requiring a templating step outside the gateway's own binary.

## Decision

`internal/envsubst.Expand` runs a regex substitution
(`\$\{VAR(:-default)?\}`) over the **raw file bytes**, before YAML parsing,
for both `config.yml` and every `api-spec.yaml` file. A reference without a
default fails the load if the variable is unset — the gateway never
silently resolves a missing variable to an empty string. `internal/dotenv`
optionally loads a `.env` file's `KEY=VALUE` pairs into the process
environment first (missing file is not an error), but only for variables
not already set in the real environment — real environment variables
always win over the `.env` file, matching conventional dotenv precedence.

## Consequences

- Expansion is purely textual and type-blind — `${GATEWAY_PORT:-8081}`
  substitutes text that then has to parse as YAML's `int`, so a bad
  default or env value surfaces as a YAML type error downstream, not an
  envsubst error directly.
- Because this runs before YAML parsing, `${VAR}` works anywhere in either
  file — inside a string, a bare scalar, even inside a comment (though a
  reference inside a comment still gets expanded, which is harmless but
  worth knowing).
