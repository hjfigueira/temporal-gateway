# See .specs/adr/active/0029-validate-requests-with-kin-openapi.md
# See .specs/adr/active/0028-exact-json-numbers-in-request-bodies.md
# See .specs/adr/deprecated/0009-json-schema-lite-validator-with-laravel-style-messages.md (history)
# Code: internal/gateway/request_validation.go, internal/gateway/dispatch_handler.go,
#       internal/spec/spec.go (kin-openapi load + merge), internal/spec/validation.go

Feature: Request validation
  The OpenAPI spec is the validator. Before anything is dispatched to
  Temporal, each request is validated against its operation with
  kin-openapi's openapi3filter: path, query and header parameters, and the
  requestBody's required flag, Content-Type and schema, using the full
  OpenAPI 3.0 schema vocabulary. A failure returns 422 VALIDATION_FAILED
  with a "fields" map naming every problem at once, not just the first.

  Background:
    Given an operation declares parameters and a requestBody with an application/json schema

  Scenario: A valid request passes through to dispatch
    Given the schema requires "orderId" and "customerId", both strings
    And the request body has both fields as strings
    When the request is validated
    Then the request proceeds to Temporal dispatch

  Scenario: A missing required field is reported by name
    Given the schema requires "orderId" and "customerId"
    And the request body only has "customerId"
    When the request is validated
    Then the response is 422 with status "VALIDATION_FAILED"
    And the response's fields map has "orderId": ["property \"orderId\" is missing"]

  Scenario: Every violation is reported, not just the first
    Given the request has a path param outside its enum, a query param above its maximum,
      a missing required field, a wrong-typed field, an array item failing its pattern,
      and an extra field the schema forbids
    When the request is validated
    Then one 422 response names all six problems
    And their fields are "path.region", "query.limit", "orderId", "customerId",
      "items[1].sku", and "_body" (for the forbidden extra field)
    # Messages are kin-openapi's own wording. An additionalProperties
    # violation is reported on its parent object; at the top level that's
    # "_body", and the message names the property.

  Scenario: Parameters are enforced, not just documented
    Given a query parameter "limit" declared as an integer with maximum 10
    When a request sends "?limit=20"
    Then the response is 422 with "query.limit" in the fields map
    And nothing is dispatched

  Scenario: requestBody.required enforces presence of a body at all
    Given requestBody.required is true
    And the request has no body
    When the request is validated
    Then the response is 422 with status "VALIDATION_FAILED"
    And the fields map has "_body": ["value is required but missing"]

  Scenario: An optional, absent body is not an error
    Given requestBody.required is false (or requestBody is absent)
    And the request has no body
    When the request is validated
    Then the request proceeds to Temporal dispatch

  Scenario: A body without a Content-Type is treated as JSON
    Given the request sends a valid JSON body with no Content-Type header
    When the request is validated
    Then it is validated against the application/json schema
    And the request proceeds to Temporal dispatch

  Scenario: A Content-Type the operation doesn't declare is rejected
    Given the operation declares only application/json
    And the request sends Content-Type "text/plain"
    When the request is validated
    Then the response is 422 with status "VALIDATION_FAILED"

  Scenario: The spec's security schemes are not enforced
    Given the spec declares an apiKey security requirement
    And the request carries no API key
    When the request is validated
    Then the request proceeds to Temporal dispatch
    # ADR-015/ADR-029: auth is out of scope for the gateway.

  Scenario: Schema defaults don't change what is dispatched
    Given a schema property declares a default value
    And the request omits that property
    When the request is dispatched
    Then the workflow input is the body exactly as sent, without the default

  Scenario: Malformed JSON in the request body is rejected before validation
    Given the request body is not valid JSON
    When the request is decoded
    Then the response is 422 with status "INVALID_REQUEST"
    And the message names the JSON decoding error

  Scenario: Data after the JSON value is rejected
    Given the request body is {"orderId":"o1"} followed by more data
    When the request is decoded
    Then the response is 422 with status "INVALID_REQUEST"
    And nothing is dispatched

  Scenario: Numbers reach Temporal exactly as sent
    Given the request body is {"quantity": 12345678901234567}
    When the request is dispatched
    Then a schema of type "integer" accepts it
    And the workflow input carries 12345678901234567, not a float64-rounded value

  Scenario: Length limits count characters, not bytes
    Given a field is declared type "string" with minLength 4 and maxLength 4
    And the request sends "José" (4 characters, 5 bytes)
    When the request is validated
    Then the request proceeds to Temporal dispatch

  Scenario Outline: A schema mistake fails spec loading
    Given a parameter or requestBody schema with <mistake>
    When the API spec is loaded
    Then loading fails naming the operation and the problem
    # ADR-008: a typo must not silently switch a check off.

    Examples:
      | mistake                       |
      | type: strnig                  |
      | pattern: "(["                 |
