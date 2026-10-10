# See .specs/adr/active/0011-var-expansion-over-raw-config-bytes.md
# See .specs/adr/active/0012-multi-file-api-spec-merge.md
# See .specs/adr/active/0008-fail-fast-validation-at-startup.md
# Code: internal/envsubst/envsubst.go, internal/dotenv/dotenv.go,
#       internal/config/spec_path.go, internal/spec/spec.go (merge), main.go

Feature: Environment-aware config, .env loading, and multi-file specs
  config.yml and api-spec.yaml both support "${VAR}" / "${VAR:-default}"
  shell-style expansion, with an optional .env file for local development.
  api-spec.yaml may also be split across multiple files, merged at load
  time. A --dry-run flag validates everything without starting the server.

  Scenario: "${VAR}" substitutes a set environment variable
    Given the environment variable GATEWAY_PORT=9090 is set
    And config.yml contains "port: ${GATEWAY_PORT:-8081}"
    When the config is loaded
    Then the resolved port is 9090

  Scenario: "${VAR:-default}" falls back when the variable is unset
    Given GATEWAY_PORT is not set in the environment
    And config.yml contains "port: ${GATEWAY_PORT:-8081}"
    When the config is loaded
    Then the resolved port is 8081

  Scenario: A reference with no default and no value fails config loading
    Given TEMPORAL_HOST is not set in the environment
    And config.yml contains "host: ${TEMPORAL_HOST}" with no fallback
    When the config is loaded
    Then loading fails with an error naming TEMPORAL_HOST
    And the gateway does not start
    # Never silently resolves to an empty string.

  Scenario: Expansion happens before YAML parsing, on raw bytes
    Given "${GATEWAY_PORT:-8081}" appears as a bare (unquoted) YAML scalar
    When the file is loaded
    Then the substitution happens first, then the result is parsed as YAML
    # So the substituted text must itself be valid YAML for that field's type

  Scenario: .env supplies defaults only for variables not already set
    Given a .env file sets ORDERS_TASK_QUEUE=from-dotenv
    And the real process environment already has ORDERS_TASK_QUEUE=from-env
    When dotenv.Load runs before config parsing
    Then the real environment's value "from-env" wins
    # Real environment variables always take precedence over the .env file.

  Scenario: A missing .env file is not an error
    Given --env points at a path that does not exist
    When the gateway starts
    Then startup proceeds normally
    And no error is raised for the missing file

  Scenario: apiSpec accepts a single path or a list of paths
    Given config.yml's apiSpec is either a bare string or a YAML list of strings
    When the config is loaded
    Then both forms decode into the same internal representation

  Scenario: A later spec file entirely replaces an earlier file's same operation
    Given base.yaml declares "POST /orders" with returnStrategy acceptPartial
    And overrides.yaml also declares "POST /orders" with returnStrategy allOrNothing
    And apiSpec: [base.yaml, overrides.yaml]
    When the specs are loaded and merged
    Then the effective "POST /orders" is overrides.yaml's definition in full
    And none of base.yaml's fields for that operation survive partially

  Scenario: An operation defined in only one file is unaffected by merging
    Given base.yaml declares "GET /orders/{orderId}" and overrides.yaml does not
    When the specs are merged
    Then "GET /orders/{orderId}" is exactly as base.yaml defined it

  Scenario: Relative apiSpec paths resolve against config.yml's directory
    Given config.yml lives in /etc/gateway/config.yml
    And apiSpec: "./api-spec.yaml"
    When paths are resolved
    Then the resolved path is /etc/gateway/api-spec.yaml
    # Regardless of the process's current working directory

  Scenario: --dry-run validates everything and exits without serving
    Given a config.yml and api-spec.yaml that are both fully valid
    And Temporal connections can be dialed
    When the gateway runs with --dry-run
    Then it loads config, loads the spec, dials Temporal, validates namespaces
    And exits 0 without binding the HTTP server

  Scenario: --dry-run does not wait for an unreachable Temporal
    Given temporal.reconnect.maxAttempts is 0 (retry forever)
    And the Temporal server is not reachable
    When the gateway runs with --dry-run
    Then it makes a single dial attempt per namespace
    And exits 1 without retrying

  Scenario: --dry-run exits non-zero on the first failure
    Given api-spec.yaml references a namespace not in temporal.connections
    When the gateway runs with --dry-run
    Then it exits 1
    And the HTTP server is never started
