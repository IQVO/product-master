Feature: Register a product
  A SKU is registered explicitly before anything else can be said about it.
  Every write is an idempotent PUT: repeating it changes nothing, bumps no
  version and publishes no event.

  Scenario: Registering a new SKU creates the product at version 1
    When I PUT "/products/SKU-1" with body '{"description":"Lithium battery pack 12V"}'
    Then the response status is 201
    And the response field "sku" is "SKU-1"
    And the response field "version" is "1"
    And the response field "physicalProfile.effectiveSource" is "none"
    And the response field "classification" is absent
    And the outbox event types are "ProductRegistered"

  Scenario: Changing the description of a registered SKU
    Given the product "SKU-1" is registered with description "Battery"
    When I PUT "/products/SKU-1" with body '{"description":"Battery 12V, 7Ah"}'
    Then the response status is 200
    And the response field "description" is "Battery 12V, 7Ah"
    And the response field "version" is "2"
    And the outbox event types are "ProductRegistered, ProductDescriptionChanged"

  Scenario: Repeating the same registration changes nothing
    Given the product "SKU-1" is registered with description "Battery"
    When I PUT "/products/SKU-1" with body '{"description":"Battery"}'
    Then the response status is 200
    And the response field "version" is "1"
    And the outbox event types are "ProductRegistered"

  Scenario: Reading a registered product
    Given the product "SKU-1" is registered with description "Battery"
    When I GET "/products/SKU-1"
    Then the response status is 200
    And the response field "description" is "Battery"

  Scenario: Reading an unknown SKU
    When I GET "/products/SKU-UNKNOWN"
    Then the response status is 404
    And the problem type is "product-not-found"

  Scenario Outline: Invalid registrations are rejected
    When I PUT "<path>" with body '<body>'
    Then the response status is 400
    And the problem type is "<slug>"
    And the outbox is empty

    Examples:
      | path                 | body                     | slug                |
      | /products/bad%20sku  | {}                       | invalid-sku         |
      | /products/SKU-1      | {"description":"a\u0007"} | invalid-description |
      | /products/SKU-1      | {"title":"x"}            | malformed-request   |
      | /products/SKU-1      | {"description":          | malformed-request   |
