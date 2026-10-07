Feature: Classify a product
  The handling classification moved here from inventory-storage (ADR 0003)
  with its closed taxonomy unchanged: a non-empty set of handling tags, a
  temperature class if and only if TemperatureSensitive, and an optional DOT
  hazard class (1-9) only with Hazmat.

  Background:
    Given the product "SKU-1" is registered with description "Battery"

  Scenario: The first classification is created (201) and published
    When I PUT "/products/SKU-1/classification" with body '{"handlingTags":["TemperatureSensitive","Hazmat"],"temperatureClass":"Frozen","dotHazardClass":3}'
    Then the response status is 201
    And the response field "handlingTags" is "Hazmat,TemperatureSensitive"
    And the response field "temperatureClass" is "Frozen"
    And the response field "dotHazardClass" is "3"
    And the response field "classificationSource" is "native"
    And the response field "version" is "2"
    And the outbox event types are "ProductRegistered, ProductClassified"
    And the last published ProductClassified data is '{"sku":"SKU-1","handling_tags":["Hazmat","TemperatureSensitive"],"temperature_class":"Frozen","dot_hazard_class":3,"classification_source":"native","version":2}'

  Scenario: Replacing a classification answers 200
    Given "SKU-1" is classified with tags "Fragile"
    When I PUT "/products/SKU-1/classification" with body '{"handlingTags":["Oversized","HighValue"]}'
    Then the response status is 200
    And the response field "handlingTags" is "Oversized,HighValue"
    And the response field "version" is "3"

  Scenario: An identical classification changes nothing
    Given "SKU-1" is classified with tags "Fragile"
    When I PUT "/products/SKU-1/classification" with body '{"handlingTags":["Fragile"]}'
    Then the response status is 200
    And the response field "version" is "2"
    And the outbox event types are "ProductRegistered, ProductClassified"

  Scenario: Reading the classification of a product that has none
    When I GET "/products/SKU-1/classification"
    Then the response status is 404
    And the problem type is "product-classification-not-found"

  Scenario: Classifying an unregistered SKU
    When I PUT "/products/SKU-404/classification" with body '{"handlingTags":["Fragile"]}'
    Then the response status is 404
    And the problem type is "product-not-found"

  Scenario Outline: Every rule of the taxonomy is enforced
    When I PUT "/products/SKU-1/classification" with body '<body>'
    Then the response status is 400
    And the problem type is "<slug>"
    And the outbox event types are "ProductRegistered"

    Examples:
      | body                                                                | slug                             |
      | {"handlingTags":[]}                                                 | no-handling-tags                 |
      | {"handlingTags":["Sharp"]}                                          | unknown-handling-tag             |
      | {"handlingTags":["Fragile","Fragile"]}                              | duplicate-handling-tag           |
      | {"handlingTags":["TemperatureSensitive"]}                           | temperature-class-required       |
      | {"handlingTags":["Fragile"],"temperatureClass":"Chilled"}           | temperature-class-not-applicable |
      | {"handlingTags":["TemperatureSensitive"],"temperatureClass":"Warm"} | unknown-temperature-class        |
      | {"handlingTags":["Hazmat"],"dotHazardClass":10}                     | invalid-dot-hazard-class         |
      | {"handlingTags":["Fragile"],"dotHazardClass":3}                     | dot-hazard-class-not-applicable  |
      | {"handlingTags":["Fragile"],"note":"x"}                             | malformed-request                |
