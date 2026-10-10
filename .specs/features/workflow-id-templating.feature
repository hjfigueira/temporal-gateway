# See .specs/adr/active/0004-origin-field-placeholders-for-workflow-id-templating.md
# See .specs/adr/active/0020-unresolved-workflow-id-placeholders-are-rejected.md
# See .specs/adr/active/0025-fingerprint-placeholder-and-nested-body-paths.md
# See .specs/adr/active/0028-exact-json-numbers-in-request-bodies.md
# Code: internal/templating (parse + render), internal/spec/validation.go
#       (validateWorkflowIDTemplate), internal/gateway/dispatch_handler.go

Feature: Workflow ID templating
  A trigger's workflowId may embed "{origin.field}" placeholders, resolved per request from the incoming HTTP call: path,
  body, query, or header (body paths may nest: "{body.items[2].sku}") - plus
  the reserved "{uuidv7}" keyword, which needs no origin and generates a
  fresh UUIDv7 per occurrence, and "{fingerprint(ref)}", a stable hash of
  any referenced value or of the whole body. A placeholder that
  can never resolve fails spec loading; one the request can't satisfy fails
  that request with 422 before anything is dispatched.

  Background:
    Given a trigger's workflowId is a template string containing placeholders

  Scenario Outline: Resolving each placeholder origin
    Given workflowId is "<template>"
    And the request has <origin> "<field>" = "<value>"
    When the template is rendered
    Then the resulting workflow ID is "<rendered>"

    Examples:
      | template                      | origin | field      | value  | rendered          |
      | order-{path.orderId}          | path   | orderId    | o1     | order-o1          |
      | order-{body.customerId}       | body   | customerId | c1     | order-c1          |
      | order-{query.filter}          | query  | filter     | active | order-active      |
      | order-{header.X-Request-Id}   | header | X-Request-Id | req-9 | order-req-9      |

  Scenario: Multiple placeholders in one template
    Given workflowId is "order-{path.orderId}-{body.customerId}"
    And path param "orderId" is "1234" and body field "customerId" is "cust-1"
    When the template is rendered
    Then the resulting workflow ID is "order-1234-cust-1"

  Scenario: {uuidv7} generates a fresh value with no origin needed
    Given workflowId is "job-{uuidv7}"
    When the template is rendered twice for two separate requests
    Then each rendering produces a different, valid UUIDv7
    And neither rendering depends on any request field being present

  Scenario: An unresolved placeholder rejects the request without dispatching
    Given an operation with two triggers whose workflowIds use "{body.orderId}"
    And the request body has no "orderId"
    When the request is handled
    Then the response is 422 with status "INVALID_REQUEST"
    And the message names "{body.orderId}"
    And neither trigger is dispatched

  Scenario Outline: A non-string JSON body field stringifies for use in a template
    Given workflowId is "order-{body.value}"
    And the request body is {"value": <json>}
    When the template is rendered
    Then the resulting workflow ID is "order-<rendered>"
    And rendering does not error or reject the request
    # Numbers keep the exact digits sent (ADR-028), never exponent form or
    # float64 rounding; objects and arrays render as compact JSON.

    Examples:
      | json              | rendered          |
      | 10000000          | 10000000          |
      | 12345678901234567 | 12345678901234567 |
      | true              | true              |
      | {"b":2,"a":1}     | {"a":1,"b":2}     |

  Scenario: An unknown origin fails spec loading
    Given workflowId contains "{cookie.sessionId}"
    When the API spec is loaded
    Then loading fails naming the unknown origin "cookie"
    # Only path/body/query/header (case-insensitive), uuidv7, and
    # fingerprint(...) are valid.

  Scenario: A path placeholder must name one of the route's path parameters
    Given the route "/orders/{orderId}" and workflowId "order-{path.id}"
    When the API spec is loaded
    Then loading fails saying {path.id} names no path parameter of this route

  Scenario: A placeholder without an origin fails spec loading
    Given workflowId is "order-{orderId}"
    When the API spec is loaded
    Then loading fails saying it must be {uuidv7} or {origin.field}

  Scenario Outline: Body paths reach nested fields and array elements
    Given the request body is {"customer":{"id":"c1"},"items":[{"sku":"a"},{"sku":"b"}]}
    And workflowId is "<template>"
    When the template is rendered
    Then the resulting workflow ID is "<rendered>"

    Examples:
      | template                 | rendered   |
      | cust-{body.customer.id}  | cust-c1    |
      | sku-{body.items[1].sku}  | sku-b      |

  Scenario: A body path that doesn't exist rejects the request
    Given workflowId is "sku-{body.items[5].sku}"
    And the body's "items" has only 2 elements
    When the request is handled
    Then the response is 422 with status "INVALID_REQUEST"
    And nothing is dispatched

  Scenario: fingerprint(body) is stable for the same content
    Given workflowId is "order-{fingerprint(body)}"
    When two requests send {"a":1,"b":"x"} and {"b":"x","a":1.0}
    Then both render the same 16-hex-character workflow ID suffix
    # SHA-256 of the canonical JSON (sorted keys, numbers as float64),
    # first 16 hex chars. Unchanged by ADR-028's exact-number decoding.
    But a request with {"a":2,"b":"x"} renders a different one

  Scenario: fingerprint can target one value
    Given workflowId is "line-{path.orderId}-{fingerprint(body.items[2])}"
    When the template is rendered
    Then only the third item affects the fingerprint
    And path, query, or header values can be fingerprinted the same way

  Scenario: Only workflowId is templated
    Given a startWorkflow trigger with memo {"requestedBy": "{body.customerId}"}
    When the trigger dispatches
    Then the memo value sent to Temporal is the literal text "{body.customerId}"

  Scenario: A bare {body} fails spec loading
    Given workflowId is "order-{body}"
    When the API spec is loaded
    Then loading fails suggesting {fingerprint(body)} or a field like {body.id}

  Scenario Outline: A malformed path fails spec loading
    Given workflowId is "<template>"
    When the API spec is loaded
    Then loading fails naming the problem

    Examples:
      | template                   |
      | x-{body.items[2}           |
      | x-{body.items[-1]}         |
      | x-{fingerprint(body}       |
