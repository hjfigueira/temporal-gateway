# ADR-029: Validate requests from the OpenAPI spec with kin-openapi

**Status:** Accepted. Supersedes [ADR-009](../deprecated/0009-json-schema-lite-validator-with-laravel-style-messages.md); extends [ADR-008](0008-fail-fast-validation-at-startup.md) and [ADR-012](0012-multi-file-api-spec-merge.md).

**Related features:** [`request-validation.feature`](../../features/request-validation.feature)

## Context

OpenAPI schemas are JSON Schema, and the gateway already requires an
OpenAPI spec. ADR-009 nevertheless validated request bodies with a
hand-rolled subset of JSON Schema (`internal/validate`), to get
Laravel-style messages and avoid a dependency. That meant:

- a second implementation of JSON Schema to maintain, which kept growing
  fixes (unknown types failing open, regexes compiled per request, byte
  lengths instead of characters, float64 numbers);
- most of the spec ignored: `oneOf`/`allOf`/`$ref`/`format`/`nullable`,
  and every parameter (path, query, header) was "documentation only";
- the spec parsed into hand-written structs that only modeled what the
  validator understood.

## Decision

- **The spec is the validator.** `spec.Load` parses each file with
  kin-openapi (`openapi3`) alongside the gateway's own x-temporal structs,
  merging documents with the same operation-granularity override as
  ADR-012 (each file's `$ref`s are resolved within that file before the
  merge). Every `spec.Route` carries kin's `routers.Route` for its
  operation.
- **Per request**, `internal/gateway.validateRequest` runs
  `openapi3filter.ValidateRequest`: path, query, header and cookie
  parameters, the body's `required` flag, its `Content-Type`, and its
  schema, with:
  - `MultiError` - every problem is reported, flattened into the existing
    422 `VALIDATION_FAILED` response's `fields` map (`"query.limit"`,
    `"items[1].sku"`, `"_body"`); messages are kin-openapi's own.
  - `SkipSettingDefaults` - schema defaults are not written into the
    request, so the dispatched body is exactly what the client sent
    (ADR-028).
  - `NoopAuthenticationFunc` - the spec's `security` schemes stay
    unenforced, consistent with
    [ADR-015](0015-auth-and-middleware-config-not-yet-enforced.md); auth may
    live in a different service.
  - A body with no `Content-Type` is treated as `application/json`, since
    the gateway only speaks JSON and existing clients may not send one.
- The gateway still reads and decodes the body itself first (exact numbers,
  413 on oversize, 422 on malformed JSON or trailing data - ADR-022,
  ADR-028), then hands kin the same bytes.
- **At load** (ADR-008), every parameter and request body is validated by
  kin-openapi, so an unknown `type` or a `pattern` that doesn't compile fails
  startup. Responses are not validated: the gateway never checks them, and
  requiring them would reject specs that load today.
- `internal/validate` is deleted, along with `spec.RequestBody`,
  `spec.Parameter` and `spec.MediaType`.

## Consequences

- Error messages change from Laravel style ("The orderId field is
  required.") to kin-openapi's (`property "orderId" is missing`). The
  response shape (status, message, `fields`) is unchanged. A missing
  required body is now `VALIDATION_FAILED` with a `_body` entry, not
  `INVALID_REQUEST`.
- An undeclared property under `additionalProperties: false` is reported
  on its parent object (`_body` at the top level), not under its own name.
- Parameters are now enforced: a request that violates a declared path,
  query or header parameter schema (or omits a required one) gets 422
  where it used to be dispatched.
- A request whose `Content-Type` isn't declared in the operation's
  `requestBody.content` gets 422.
- The full OpenAPI 3.0 schema vocabulary applies (`oneOf`, `allOf`, `$ref`,
  `nullable`, ...). `type: null`, which the old validator accepted but
  OpenAPI 3.0 doesn't define, now fails spec load.
- kin-openapi and its dependencies add about 2.4 MB (~10%) to the stripped
  binary.
