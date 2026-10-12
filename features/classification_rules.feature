# Derived from: apis/openapi.yaml (classifyProduct, ClassifyProductRequest), docs/adr/0003,
# internal/domain/product/classification.go (closed taxonomy), apis/asyncapi.yaml (ProductClassified)
@bdd
Feature: Classification rules beyond the first PUT
  The closed taxonomy of ADR 0003: tags are a set reported in a stable order,
  the DOT hazard class is 1..9 and optional, and a classification that is
  rejected or identical never disturbs what is already stored.

  Background:
    Given the product "SKU-1" is registered with description "Battery"

  Scenario Outline: The DOT hazard class is optional and accepted from 1 to 9
    When I PUT "/products/SKU-1/classification" with body '<body>'
    Then the response status is 201
    And the response field "<field>" is "<value>"

    Examples:
      | body                                       | field          | value |
      | {"handlingTags":["Hazmat"],"dotHazardClass":1} | dotHazardClass | 1     |
      | {"handlingTags":["Hazmat"],"dotHazardClass":9} | dotHazardClass | 9     |
      | {"handlingTags":["Hazmat"]}                    | handlingTags   | Hazmat |

  Scenario Outline: A DOT hazard class outside 1..9 is rejected
    When I PUT "/products/SKU-1/classification" with body '<body>'
    Then the response status is 400
    And the problem type is "invalid-dot-hazard-class"
    And the outbox event types are "ProductRegistered"

    Examples:
      | body                                            |
      | {"handlingTags":["Hazmat"],"dotHazardClass":0}  |
      | {"handlingTags":["Hazmat"],"dotHazardClass":-1} |

  Scenario: Tags are reported in the stable enum order whatever the request order
    When I PUT "/products/SKU-1/classification" with body '{"handlingTags":["HighValue","Oversized","TemperatureSensitive","Fragile","Hazmat"],"temperatureClass":"Chilled"}'
    Then the response status is 201
    And the response field "handlingTags" is "Hazmat,Fragile,TemperatureSensitive,Oversized,HighValue"
    And the response field "temperatureClass" is "Chilled"

  Scenario: Replacing a classification can drop the temperature class and the DOT class
    Given I PUT "/products/SKU-1/classification" with body '{"handlingTags":["Hazmat","TemperatureSensitive"],"temperatureClass":"Ambient","dotHazardClass":3}'
    When I PUT "/products/SKU-1/classification" with body '{"handlingTags":["Fragile"]}'
    Then the response status is 200
    And the response field "temperatureClass" is absent
    And the response field "dotHazardClass" is absent
    And the response field "version" is "3"
    And the last published ProductClassified data is '{"sku":"SKU-1","handling_tags":["Fragile"],"classification_source":"native","version":3}'

  Scenario: The same tags in another order are the same classification
    Given "SKU-1" is classified with tags "Oversized, Fragile"
    When I PUT "/products/SKU-1/classification" with body '{"handlingTags":["Fragile","Oversized"]}'
    Then the response status is 200
    And the response field "version" is "2"
    And the outbox event types are "ProductRegistered, ProductClassified"

  Scenario: A rejected classification leaves the stored one untouched
    Given "SKU-1" is classified with tags "Fragile"
    When I PUT "/products/SKU-1/classification" with body '{"handlingTags":["Sharp"]}'
    Then the response status is 400
    When I GET "/products/SKU-1/classification"
    Then the response field "handlingTags" is "Fragile"
    And the response field "version" is "2"
    And the outbox event types are "ProductRegistered, ProductClassified"
