# Derived from: apis/openapi.yaml (registerProduct, getProduct, Sku and Problem schemas),
# apis/asyncapi.yaml (ProductRegistered), docs/adr/0001, docs/adr/0004, .claude/rules/rest-api.md
@bdd
Feature: Product contract: field limits, error documents and published envelope
  The edges of the registration contract: where a description stops being
  valid, how every error is told to the caller (RFC 7807), and what the
  outbox hands to Kafka. The service clock reads 2026-10-06T15:00:00Z.

  Scenario Outline: A description is valid up to 200 characters
    When I register "SKU-1" with a description of <length> characters
    Then the response status is <status>
    And the outbox event types are "<events>"

    Examples:
      | length | status | events            |
      | 200    | 201    | ProductRegistered |
      | 201    | 400    |                   |

  Scenario Outline: Every read rejects an invalid SKU
    When I GET "<path>"
    Then the response status is 400
    And the problem type is "invalid-sku"

    Examples:
      | path                                 |
      | /products/bad%20sku                  |
      | /products/SKU%2F1                    |
      | /products/bad%20sku/classification   |
      | /products/bad%20sku/physical-profile |

  Scenario Outline: Operations on an unregistered SKU answer 404
    When I send <method> "<path>" with body '<body>'
    Then the response status is 404
    And the problem type is "product-not-found"

    Examples:
      | method | path                                  | body                                                       |
      | GET    | /products/SKU-404/classification      |                                                            |
      | GET    | /products/SKU-404/physical-profile    |                                                            |
      | PUT    | /products/SKU-404/dimensions/declared | {"lengthMm":1,"widthMm":1,"heightMm":1,"weightG":1}        |
      | PUT    | /products/SKU-404/dimensions/measured | {"lengthMm":1,"widthMm":1,"heightMm":1,"weightG":1,"measuredAt":"2026-10-06T14:00:00Z"} |

  Scenario: An error is an RFC 7807 problem document
    When I GET "/products/SKU-UNKNOWN"
    Then the response content type is "application/problem+json"
    And the response field "status" is "404"
    And the response field "title" is "Product not found"
    And the response field "detail" is "product not found"
    And the response field "instance" is "/products/SKU-UNKNOWN"

  Scenario: Registering publishes a CloudEvent keyed by the SKU
    When I PUT "/products/SKU-1" with body '{"description":"Lithium battery pack 12V"}'
    Then the last published "ProductRegistered" event data is '{"sku":"SKU-1","description":"Lithium battery pack 12V","version":1}'
    And the last outbox message is a CloudEvent on topic "warehouse.product-master.events" keyed "SKU-1" about "SKU-1" at "2026-10-06T15:00:00Z" with data schema "urn:warehouse:product-master:events:ProductRegistered:v1"

  Scenario: The probes report liveness separately from readiness
    When I GET "/healthz"
    Then the response status is 200
    And the response field "status" is "ok"
    When I GET "/readyz"
    Then the response status is 200
    And the response field "status" is "ready"
    Given the service is draining for shutdown
    When I GET "/readyz"
    Then the response status is 503
    And the response field "status" is "not_ready"
    When I GET "/healthz"
    Then the response status is 200
