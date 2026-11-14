# Derived from: warehouse-docs product-master (use-cases: ClassifyProduct, aggregate-design-canvas:
# Enforced Invariants, ubiquitous-language: Handling tag / Temperature class / DOT hazard class /
# Classification source, troubleshooting: REST errors), .claude/rules/domain-model.md
@bdd
Feature: Classification rules from the documentation
  The closed taxonomy, the order in which a classify request is judged, and
  what counts as a different classification. The service clock reads
  2026-10-06T15:00:00Z.

  Background:
    Given the product "SKU-1" is registered with description "Battery"

  Scenario Outline: An invalid classification is a 400 even when the SKU is not registered
    When I PUT "/products/SKU-404/classification" with body '<body>'
    Then the response status is 400
    And the problem type is "<slug>"
    And the outbox event types are "ProductRegistered"

    Examples:
      | body                                            | slug                            |
      | {"handlingTags":[]}                             | no-handling-tags                |
      | {"handlingTags":["Sharp"]}                      | unknown-handling-tag            |
      | {"handlingTags":["Fragile"],"dotHazardClass":3} | dot-hazard-class-not-applicable |

  Scenario Outline: Every tag, temperature class and a DOT class next to other tags are accepted
    When I PUT "/products/SKU-1/classification" with body '<body>'
    Then the response status is 201
    And the response field "<field>" is "<value>"

    Examples:
      | body                                                                    | field            | value    |
      | {"handlingTags":["Fragile"]}                                            | handlingTags     | Fragile  |
      | {"handlingTags":["Oversized"]}                                          | handlingTags     | Oversized |
      | {"handlingTags":["HighValue"]}                                          | handlingTags     | HighValue |
      | {"handlingTags":["TemperatureSensitive"],"temperatureClass":"Ambient"}  | temperatureClass | Ambient  |
      | {"handlingTags":["TemperatureSensitive"],"temperatureClass":"Chilled"}  | temperatureClass | Chilled  |
      | {"handlingTags":["Fragile","Hazmat"],"dotHazardClass":8}                | dotHazardClass   | 8        |

  Scenario Outline: Tag and class names are exact, case-sensitive spellings
    When I PUT "/products/SKU-1/classification" with body '<body>'
    Then the response status is 400
    And the problem type is "<slug>"
    And the outbox event types are "ProductRegistered"

    Examples:
      | body                                                                  | slug                      |
      | {"handlingTags":["hazmat"]}                                           | unknown-handling-tag      |
      | {"handlingTags":[""]}                                                 | unknown-handling-tag      |
      | {"handlingTags":["TemperatureSensitive"],"temperatureClass":"frozen"} | unknown-temperature-class |

  Scenario Outline: Any difference in the tags, the temperature class or the DOT class is a new classification
    Given the PUT "/products/SKU-1/classification" with body '<first>' has been accepted
    When I PUT "/products/SKU-1/classification" with body '<second>'
    Then the response status is 200
    And the response field "version" is "3"
    And the outbox event types are "ProductRegistered, ProductClassified, ProductClassified"

    Examples:
      | first                                                                  | second                                                                 |
      | {"handlingTags":["Hazmat"],"dotHazardClass":3}                         | {"handlingTags":["Hazmat"],"dotHazardClass":4}                         |
      | {"handlingTags":["TemperatureSensitive"],"temperatureClass":"Chilled"} | {"handlingTags":["TemperatureSensitive"],"temperatureClass":"Frozen"}  |
      | {"handlingTags":["Fragile"]}                                           | {"handlingTags":["Fragile","HighValue"]}                               |
      | {"handlingTags":["Fragile","HighValue"]}                               | {"handlingTags":["Fragile","Oversized"]}                               |

  Scenario: The product read model shows the native classification next to the physical profile
    Given "SKU-1" has declared dimensions 200 x 120 x 80 mm and 1500 g
    And "SKU-1" is classified with tags "TemperatureSensitive" and temperature class "Chilled"
    When I GET "/products/SKU-1"
    Then the response status is 200
    And the response field "version" is "3"
    And the response field "classification.handlingTags" is "TemperatureSensitive"
    And the response field "classification.temperatureClass" is "Chilled"
    And the response field "classification.classificationSource" is "native"
    And the response field "physicalProfile.effectiveSource" is "declared"
