# See .specs/adr/active/0009-json-schema-lite-validator-with-laravel-style-messages.md
# Code: internal/gateway/body_validation.go, internal/validate/validate.go,
#       internal/validate/checks.go, internal/validate/messages.go

Feature: Request body validation
  The JSON request body is validated against the operation's
  requestBody.content["application/json"].schema before anything is
  dispatched to Temporal. A failure returns 422 with a structured,
  field-by-field error naming every invalid field at once - not just the
  first one found. Only a deliberately partial subset of JSON Schema is
  implemented: type, required, properties, additionalProperties, items,
  enum, minLength/maxLength, minimum/maximum, pattern.

  Background:
    Given an operation declares a requestBody with an application/json schema

  Scenario: A valid body passes through to dispatch
    Given the schema requires "orderId" and "customerId", both strings
    And the request body has both fields as strings
    When the body is validated
    Then validation reports status "VALID"
    And the request proceeds to Temporal dispatch

  Scenario: A missing required field is reported by name
    Given the schema requires "orderId" and "customerId"
    And the request body only has "orderId"
    When the body is validated
    Then validation fails with status "VALIDATION_FAILED"
    And the response's fields map includes "customerId"
    And the message is "The customerId field is required."

  Scenario: Every violation is reported, not just the first
    Given the schema requires "orderId" and "customerId"
    And the request body has neither field, and an extra field schema forbids
    When the body is validated
    Then the response names all three problems in one 422 response
    # Laravel-style messages, substituting the field path for ":attribute".

  Scenario: Type mismatch stops deeper checks on that node only
    Given a field is declared type "string" with a minLength
    And the request sends that field as a number instead
    When the body is validated
    Then only the type violation is reported for that field
    And minLength is not also evaluated against the wrong-typed value
    But sibling fields are still fully validated independently

  Scenario: requestBody.required enforces presence of a body at all
    Given requestBody.required is true
    And the request has no body
    When the body is validated
    Then validation fails with status "INVALID_REQUEST"
    And the message is "request body is required"

  Scenario: An optional, absent body is not an error
    Given requestBody.required is false (or requestBody is absent)
    And the request has no body
    When the body is validated
    Then validation reports status "VALID"

  Scenario: Malformed JSON in the request body is rejected before schema validation
    Given the request body is not valid JSON
    When the request is decoded
    Then the response is 422 with status "INVALID_REQUEST"
    And the message names the JSON decoding error

  Scenario: additionalProperties: false rejects an undeclared field
    Given the schema sets additionalProperties: false
    And declares only "orderId" under properties
    And the request body includes an extra field "secretDiscount"
    When the body is validated
    Then validation fails naming "secretDiscount" as prohibited

  Scenario: Array items are validated element-by-element with indexed paths
    Given the schema declares "items" as an array of strings
    And the request sends an array with one non-string element at index 1
    When the body is validated
    Then the violation's field path is "items[1]"

  Scenario: Schema keywords outside the supported subset are silently ignored
    Given the schema uses "oneOf" or "$ref" or "format"
    When the body is validated
    Then those keywords have no effect on the validation outcome
    And no error reports them as unsupported
    # Deliberate - see ADR-009. A spec author relying on these gets no
    # warning that they're no-ops.
