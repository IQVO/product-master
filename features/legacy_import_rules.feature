# Derived from: docs/adr/0003 (Stage A: legacy importer), apis/asyncapi.yaml
# (consumed ProductClassified of inventory-storage), internal/application/usecases/import_legacy_classification.go
@bdd
Feature: Legacy importer: consumer paths beyond the first message
  The importer turns inventory-storage's ProductClassified events into
  product-master data. These scenarios seed facts through the same use case
  the Kafka consumer calls and assert only through REST and the outbox.

  Scenario: A legacy classification for a registered product keeps its description
    Given the product "SKU-1" is registered with description "Battery"
    When inventory-storage publishes a legacy classification of "SKU-1" with tags "Fragile"
    And I GET "/products/SKU-1"
    Then the response field "description" is "Battery"
    And the response field "classification.classificationSource" is "legacy-import"
    And the response field "version" is "2"
    And the outbox event types are "ProductRegistered, ProductClassified"

  Scenario: An identical legacy classification under a new event id changes nothing
    Given inventory-storage publishes a legacy classification of "SKU-9" with tags "Fragile"
    When inventory-storage publishes a legacy classification of "SKU-9" with tags "Fragile"
    And I GET "/products/SKU-9/classification"
    Then the response field "version" is "2"
    And the outbox event types are "ProductRegistered, ProductClassified"

  Scenario: The temperature class of a legacy classification is carried over and published
    When inventory-storage publishes a legacy classification of "SKU-9" with tags "TemperatureSensitive" and temperature class "Frozen"
    And I GET "/products/SKU-9/classification"
    Then the response field "temperatureClass" is "Frozen"
    And the response field "classificationSource" is "legacy-import"
    And the last published ProductClassified data is '{"sku":"SKU-9","handling_tags":["TemperatureSensitive"],"temperature_class":"Frozen","classification_source":"legacy-import","version":2}'

  Scenario: Redelivering an older legacy event never reverts a newer classification
    Given inventory-storage publishes a legacy classification of "SKU-9" with tags "Fragile"
    And inventory-storage publishes a legacy classification of "SKU-9" with tags "Oversized"
    When inventory-storage redelivers legacy event number 1
    And I GET "/products/SKU-9/classification"
    Then the response field "handlingTags" is "Oversized"
    And the response field "version" is "3"
    And the outbox event types are "ProductRegistered, ProductClassified, ProductClassified"

  Scenario Outline: A legacy classification that breaks the taxonomy is skipped
    When inventory-storage publishes a legacy classification of "SKU-9" with tags "<tags>" and DOT hazard class <dot>
    And I GET "/products/SKU-9"
    Then the response status is 404
    And the outbox is empty

    Examples:
      | tags            | dot |
      | Hazmat          | 10  |
      | Fragile         | 3   |
      | TemperatureSensitive | 0 |
      | Fragile,Fragile | 0   |
      |                 | 0   |
