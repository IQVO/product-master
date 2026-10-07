Feature: Import legacy classifications from inventory-storage
  During the migration (ADR 0003) the legacy importer turns
  inventory-storage's ProductClassified events into product-master data.
  product-master is the authority: a legacy message never overwrites a
  classification authored here (source native).

  Scenario: An unknown SKU is registered and classified as legacy-import
    When inventory-storage publishes a legacy classification of "SKU-9" with tags "Hazmat" and DOT hazard class 9
    And I GET "/products/SKU-9/classification"
    Then the response status is 200
    And the response field "classificationSource" is "legacy-import"
    And the response field "dotHazardClass" is "9"
    And the response field "version" is "2"
    And the outbox event types are "ProductRegistered, ProductClassified"

  Scenario: A legacy message never overwrites a native classification
    Given the product "SKU-1" is registered with description "Battery"
    And "SKU-1" is classified with tags "Fragile"
    When inventory-storage publishes a legacy classification of "SKU-1" with tags "Hazmat"
    And I GET "/products/SKU-1/classification"
    Then the response field "handlingTags" is "Fragile"
    And the response field "classificationSource" is "native"
    And the response field "version" is "2"
    And the outbox event types are "ProductRegistered, ProductClassified"

  Scenario: A later legacy message replaces an earlier legacy one
    When inventory-storage publishes a legacy classification of "SKU-9" with tags "Fragile"
    And inventory-storage publishes a legacy classification of "SKU-9" with tags "Oversized"
    And I GET "/products/SKU-9/classification"
    Then the response field "handlingTags" is "Oversized"
    And the response field "classificationSource" is "legacy-import"
    And the response field "version" is "3"

  Scenario: Confirming a legacy classification natively makes it native
    Given inventory-storage publishes a legacy classification of "SKU-9" with tags "Fragile"
    When I PUT "/products/SKU-9/classification" with body '{"handlingTags":["Fragile"]}'
    Then the response status is 200
    And the response field "classificationSource" is "native"
    And the response field "version" is "3"

  Scenario: A redelivered legacy event is processed once
    When inventory-storage publishes a legacy classification of "SKU-9" with tags "Fragile"
    And inventory-storage redelivers the same legacy event
    Then the outbox event types are "ProductRegistered, ProductClassified"

  Scenario: An invalid legacy classification is skipped
    When inventory-storage publishes a legacy classification of "SKU-9" with tags "Sharp"
    And I GET "/products/SKU-9"
    Then the response status is 404
    And the outbox is empty
