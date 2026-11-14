# Derived from: warehouse-docs product-master (use-cases: RegisterProduct and the invariants table,
# aggregate-design-canvas: Enforced Invariants + State Transitions, ubiquitous-language: SKU,
# Description, Version, No change, troubleshooting: REST errors), docs/docs/ddd/use-cases.md
@bdd
Feature: Registration rules from the documentation
  A SKU is 1 to 64 characters, a description at most 200; re-registering a SKU
  only replaces its description; the version grows by one per accepted change
  and a repeated command changes nothing. The service clock reads
  2026-10-06T15:00:00Z.

  Scenario Outline: A SKU is 1 to 64 characters, counted as characters and not as bytes
    When I register a SKU made of <count> times "<unit>"
    Then the response status is <status>
    And the outbox event types are "<events>"

    Examples:
      | count | unit | status | events            |
      | 64    | S    | 201    | ProductRegistered |
      | 65    | S    | 400    |                   |
      | 64    | é    | 201    | ProductRegistered |
      | 65    | é    | 400    |                   |

  Scenario Outline: A SKU with a slash, a control character or any kind of space is invalid
    When I PUT "/products/<sku>" with body '{"description":"x"}'
    Then the response status is 400
    And the problem type is "invalid-sku"
    And the outbox is empty

    Examples:
      | sku        | spelled as        |
      | SKU%2F1    | an encoded slash  |
      | SKU%071    | a bell character  |
      | SKU%091    | a tab             |
      | SKU%0A1    | a line feed       |
      | SKU%C2%A01 | a no-break space  |

  Scenario Outline: A description is 200 characters at most, counted as characters and not as bytes
    When I register "SKU-1" with a description made of <count> times "<unit>"
    Then the response status is <status>
    And the outbox event types are "<events>"

    Examples:
      | count | unit | status | events            |
      | 200   | é    | 201    | ProductRegistered |
      | 201   | é    | 400    |                   |

  Scenario Outline: A line feed, a tab or a DEL in a description is a control character
    When I PUT "/products/SKU-1" with body '{"description":"<text>"}'
    Then the response status is 400
    And the problem type is "invalid-description"
    And the outbox is empty

    Examples:
      | text           |
      | two\u000alines |
      | col1\u0009col2 |
      | del\u007f      |

  Scenario: A product may be registered without a description, and registering it again with none changes nothing
    When I PUT "/products/SKU-1" with body '{}'
    Then the response status is 201
    And the response field "description" is ""
    When I PUT "/products/SKU-1" with body '{}'
    Then the response status is 200
    And the response field "version" is "1"
    And the outbox event types are "ProductRegistered"

  Scenario: Clearing a description is a change published with the new, empty text
    Given the product "SKU-1" is registered with description "Battery"
    When I PUT "/products/SKU-1" with body '{}'
    Then the response status is 200
    And the response field "description" is ""
    And the response field "version" is "2"
    And the last published "ProductDescriptionChanged" event data is '{"sku":"SKU-1","description":"","version":2}'

  Scenario: Re-registering a SKU replaces only its description
    Given the product "SKU-1" is registered with description "Battery"
    And "SKU-1" is classified with tags "Hazmat"
    And "SKU-1" has declared dimensions 200 x 120 x 80 mm and 1500 g
    When I PUT "/products/SKU-1" with body '{"description":"Battery 12V, 7Ah"}'
    Then the response status is 200
    And the response field "description" is "Battery 12V, 7Ah"
    And the response field "version" is "4"
    And the response field "classification.handlingTags" is "Hazmat"
    And the response field "physicalProfile.declared.weightG" is "1500"
    And the outbox event types are "ProductRegistered, ProductClassified, ProductDimensionsDeclared, ProductDescriptionChanged"

  Scenario Outline: A request body is at most 64 KiB
    When I PUT "/products/SKU-1" with a JSON body padded with leading spaces to <bytes> bytes: '{"description":"x"}'
    Then the response status is <status>
    And the outbox event types are "<events>"

    Examples:
      | bytes | status | events            |
      | 65536 | 201    | ProductRegistered |
      | 65537 | 400    |                   |

  Scenario Outline: A body is exactly one JSON object
    When I PUT "/products/SKU-1" with body '<body>'
    Then the response status is 400
    And the problem type is "malformed-request"
    And the outbox is empty

    Examples:
      | body                                         |
      | {"description":"x"}{"description":"y"}      |
      | ["Battery"]                                  |

  Scenario: Every accepted change adds one to the version and a repeated command adds nothing
    Given the product "SKU-1" is registered with description "Battery"
    And "SKU-1" is classified with tags "Fragile"
    And "SKU-1" is classified with tags "Fragile"
    And "SKU-1" has declared dimensions 200 x 120 x 80 mm and 1500 g
    And "SKU-1" has declared dimensions 200 x 120 x 80 mm and 1500 g
    And "SKU-1" was measured at "2026-10-06T14:05:00Z"
    When I PUT "/products/SKU-1" with body '{"description":"Battery 12V"}'
    Then the outbox event versions are "1, 2, 3, 4, 5"
    And the outbox event types are "ProductRegistered, ProductClassified, ProductDimensionsDeclared, ProductMeasured, ProductDescriptionChanged"
    When I GET "/products/SKU-1"
    Then the response field "version" is "5"
