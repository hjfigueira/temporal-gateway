# See .specs/adr/0004-origin-field-placeholders-for-workflow-id-templating.md
# Code: internal/gateway/template.go

Feature: Workflow ID templating
  Any x-temporal string field (chiefly workflowId) may embed "{origin.field}"
  placeholders, resolved per request from the incoming HTTP call: path,
  body, query, or header - plus the reserved "{uuidv7}" keyword, which needs
  no origin and generates a fresh UUIDv7 per occurrence. A placeholder that
  can't be resolved is left untouched in the rendered string rather than
  failing the request.

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

  Scenario: An unresolved placeholder is left untouched
    Given workflowId is "order-{body.missingField}"
    And the request body has no "missingField"
    When the template is rendered
    Then the resulting workflow ID is literally "order-{body.missingField}"
    And the request is not rejected because of it

  Scenario: A non-string JSON body field stringifies for use in a template
    Given workflowId is "order-{body.metadata}"
    And body field "metadata" is a JSON object, not a string
    When the template is rendered
    Then the object renders as its Go-syntax representation
    And rendering does not error or reject the request

  Scenario: An unknown origin resolves to nothing
    Given workflowId contains "{cookie.sessionId}"
    When the template is rendered
    Then "cookie" is not a recognized origin (only path/body/query/header/uuidv7 are)
    And the placeholder is left untouched, same as an unresolved field
