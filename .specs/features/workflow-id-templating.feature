# See .specs/adr/active/0004-origin-field-placeholders-for-workflow-id-templating.md
# See .specs/adr/active/0020-unresolved-workflow-id-placeholders-are-rejected.md
# Code: internal/gateway/template.go, internal/gateway/dispatch_handler.go,
#       internal/spec/template.go

Feature: Workflow ID templating
  Any x-temporal string field (chiefly workflowId) may embed "{origin.field}"
  placeholders, resolved per request from the incoming HTTP call: path,
  body, query, or header - plus the reserved "{uuidv7}" keyword, which needs
  no origin and generates a fresh UUIDv7 per occurrence. A placeholder that
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

  Scenario: A non-string JSON body field stringifies for use in a template
    Given workflowId is "order-{body.metadata}"
    And body field "metadata" is a JSON object, not a string
    When the template is rendered
    Then the object renders as its Go-syntax representation
    And rendering does not error or reject the request

  Scenario: An unknown origin fails spec loading
    Given workflowId contains "{cookie.sessionId}"
    When the API spec is loaded
    Then loading fails naming the unknown origin "cookie"
    # Only path/body/query/header (case-insensitive) and uuidv7 are valid.

  Scenario: A path placeholder must name one of the route's path parameters
    Given the route "/orders/{orderId}" and workflowId "order-{path.id}"
    When the API spec is loaded
    Then loading fails saying {path.id} names no path parameter of this route

  Scenario: A placeholder without an origin fails spec loading
    Given workflowId is "order-{orderId}"
    When the API spec is loaded
    Then loading fails saying it must be {uuidv7} or {origin.field}
