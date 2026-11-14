# Derived from: warehouse-docs product-master (business-context: "copies every event to
# warehouse.product-master.analytics", domain-events: Analytics copy, troubleshooting: Analytics read
# side table: freshness, DLQ, skipped non-CloudEvents, invalid-report-range), ADR 0006 (projector
# policy, first-event semantics, half-open range, coverage ratios), apis/openapi.yaml (reports)
@bdd
Feature: Master data quality report and the analytics stream
  The analytics stream carries the five unit events under the same id as
  their integration message; the projector folds them into a per-day report
  of FIRST events, the open discrepancies at the end of each day and the
  current coverage. SKU-1 is registered, classified, declared and measured on
  2026-10-05 at 09:00:00Z, the measurement (10 x 10 x 10 mm, 10 g) disagreeing
  with the declared 200 x 120 x 80 mm and 1500 g.

  Background:
    Given the service clock reads "2026-10-05T09:00:00Z"
    And the product "SKU-1" is registered with description "Battery"
    And "SKU-1" is classified with tags "Fragile"
    And "SKU-1" has declared dimensions 200 x 120 x 80 mm and 1500 g
    And "SKU-1" was measured at "2026-10-05T08:30:00Z"

  Scenario: The analytics stream repeats the id, type and payload of every unit event
    Then the analytics topic holds 4 messages
    And every analytics message repeats the id, type and payload of an integration message

  Scenario: Pack level events are published on the integration topic only
    Given "SKU-1" has the each GTIN "4006381333931"
    Then the outbox holds 5 messages
    And the analytics topic holds 4 messages

  Scenario: Until the projector has applied an event the report knows no product and the freshness is null
    When I GET report "/reports/freshness"
    Then the response status is 200
    And the response field "as_of" is null
    And the response field "lag_seconds" is null
    When I GET report "/reports/master-data-quality?from=2026-10-05T00:00:00Z&to=2026-10-06T00:00:00Z"
    Then the response field "coverage.products" is "0"
    And the response field "coverage.classified_ratio" is "0"
    And the response field "coverage.discrepancy_ratio" is "0"
    And the response field "days.0.registered" is "0"

  Scenario: The freshness is the age of the newest applied event and never negative
    Given the analytics projector consumes the analytics topic
    And the service clock reads "2026-10-05T09:00:42Z"
    When I GET report "/reports/freshness"
    Then the response field "as_of" is "2026-10-05T09:00:00Z"
    And the response field "lag_seconds" is "42"
    Given the service clock reads "2026-10-05T08:00:00Z"
    When I GET report "/reports/freshness"
    Then the response field "lag_seconds" is "0"

  Scenario: A day counts the SKUs whose first such event fell in it, so reclassifying and re-measuring count nothing
    Given the service clock reads "2026-10-06T09:00:00Z"
    And "SKU-1" is classified with tags "Fragile, HighValue"
    And the PUT "/products/SKU-1/dimensions/measured" with body '{"lengthMm":201,"widthMm":120,"heightMm":80,"weightG":1500,"measuredAt":"2026-10-06T08:00:00Z"}' has been accepted
    And the analytics projector consumes the analytics topic
    When I GET report "/reports/master-data-quality?from=2026-10-05T00:00:00Z&to=2026-10-07T00:00:00Z"
    Then the response status is 200
    And the response field "days.0.day" is "2026-10-05"
    And the response field "days.0.registered" is "1"
    And the response field "days.0.classified" is "1"
    And the response field "days.0.dimensions_declared" is "1"
    And the response field "days.0.measured" is "1"
    And the response field "days.1.day" is "2026-10-06"
    And the response field "days.1.registered" is "0"
    And the response field "days.1.classified" is "0"
    And the response field "days.1.dimensions_declared" is "0"
    And the response field "days.1.measured" is "0"

  Scenario: Open discrepancies are counted at the end of each day, so a resolved one still shows on the day it was open
    Given the service clock reads "2026-10-06T09:00:00Z"
    And the PUT "/products/SKU-1/dimensions/measured" with body '{"lengthMm":201,"widthMm":120,"heightMm":80,"weightG":1500,"measuredAt":"2026-10-06T08:00:00Z"}' has been accepted
    And the analytics projector consumes the analytics topic
    When I GET report "/reports/master-data-quality?from=2026-10-05T00:00:00Z&to=2026-10-07T00:00:00Z"
    Then the response field "days.0.open_discrepancies" is "1"
    And the response field "days.1.open_discrepancies" is "0"
    And the response field "coverage.open_discrepancies" is "0"

  Scenario: The range is half-open: an event at from is in and an event at to is out
    Given the analytics projector consumes the analytics topic
    When I GET report "/reports/master-data-quality?from=2026-10-05T09:00:00Z&to=2026-10-05T10:00:00Z"
    Then the response field "days.0.registered" is "1"
    When I GET report "/reports/master-data-quality?from=2026-10-05T08:00:00Z&to=2026-10-05T09:00:00Z"
    Then the response field "days.0.registered" is "0"

  Scenario: The days are dense: every UTC day of the range is a row, zeros included
    Given the analytics projector consumes the analytics topic
    When I GET report "/reports/master-data-quality?from=2026-10-03T00:00:00Z&to=2026-10-07T00:00:00Z"
    Then the response list "days" has 4 items
    And the response field "days.0.day" is "2026-10-03"
    And the response field "days.1.registered" is "0"
    And the response field "days.2.registered" is "1"
    And the response field "days.3.registered" is "0"

  Scenario Outline: An omitted bound defaults so that the range is the 30 days ending at the clock
    Given the analytics projector consumes the analytics topic
    When I GET report "/reports/master-data-quality?<query>"
    Then the response status is 200
    And the response field "from" is "<from>"
    And the response field "to" is "<to>"
    And the response list "days" has <days> items

    Examples:
      | query                    | from                 | to                   | days |
      |                          | 2026-09-05T09:00:00Z | 2026-10-05T09:00:00Z | 31   |
      | to=2026-10-06T00:00:00Z  | 2026-09-06T00:00:00Z | 2026-10-06T00:00:00Z | 30   |
      | from=2026-10-04T00:00:00Z | 2026-10-04T00:00:00Z | 2026-10-05T09:00:00Z | 2    |

  Scenario Outline: A range that is not RFC 3339, empty or inverted is refused
    When I GET report "/reports/master-data-quality?<query>"
    Then the response status is 400
    And the problem type is "invalid-report-range"

    Examples:
      | query                                           |
      | from=yesterday                                  |
      | to=2026-13-01T00:00:00Z                         |
      | from=2026-10-05T00:00:00Z&to=2026-10-05T00:00:00Z |
      | from=2026-10-06T00:00:00Z&to=2026-10-05T00:00:00Z |

  Scenario Outline: A range is at most 366 days
    When I GET report "/reports/master-data-quality?from=<from>&to=2026-10-05T00:00:00Z"
    Then the response status is <status>

    Examples:
      | from                 | status |
      | 2025-10-04T00:00:00Z | 200    |
      | 2025-10-03T00:00:00Z | 400    |

  Scenario: The coverage is the current state of every product and the discrepancy ratio is over the measured ones
    Given the product "SKU-2" is registered with description "Charger"
    And the product "SKU-3" is registered with description "Cable"
    And "SKU-3" has declared dimensions 100 x 100 x 100 mm and 1000 g
    And I record for "SKU-3" a measurement of 100 x 100 x 100 mm and 1000 g taken at "2026-10-05T08:30:00Z"
    And the product "SKU-4" is registered with description "Case"
    And "SKU-4" is classified with tags "Oversized"
    And "SKU-4" has declared dimensions 900 x 600 x 400 mm and 9000 g
    And the analytics projector consumes the analytics topic
    When I GET report "/reports/master-data-quality?from=2026-10-05T00:00:00Z&to=2026-10-06T00:00:00Z"
    Then the response field "coverage.products" is "4"
    And the response field "coverage.classified" is "2"
    And the response field "coverage.classified_ratio" is "0.5"
    And the response field "coverage.dimensions_declared" is "3"
    And the response field "coverage.dimensions_declared_ratio" is "0.75"
    And the response field "coverage.measured" is "2"
    And the response field "coverage.measured_ratio" is "0.5"
    And the response field "coverage.open_discrepancies" is "1"
    And the response field "coverage.discrepancy_ratio" is "0.5"

  Scenario: Delivering the whole analytics topic a second time changes nothing
    Given the analytics projector consumes the analytics topic
    And the analytics topic is delivered again from the beginning
    When I GET report "/reports/master-data-quality?from=2026-10-05T00:00:00Z&to=2026-10-06T00:00:00Z"
    Then the response field "days.0.registered" is "1"
    And the response field "days.0.measured" is "1"
    And the response field "days.0.open_discrepancies" is "1"
    And the response field "coverage.products" is "1"
    And the response field "coverage.open_discrepancies" is "1"

  Scenario: The first event of a kind wins whatever the arrival order
    Given the service clock reads "2026-10-06T09:00:00Z"
    And "SKU-1" is classified with tags "Fragile, HighValue"
    And the analytics projector consumes the analytics topic newest first
    When I GET report "/reports/master-data-quality?from=2026-10-05T00:00:00Z&to=2026-10-07T00:00:00Z"
    Then the response field "days.0.registered" is "1"
    And the response field "days.0.classified" is "1"
    And the response field "days.1.classified" is "0"

  Scenario: An older physical profile arriving late does not roll the current coverage back
    Given the analytics projector consumes the analytics topic newest first
    When I GET report "/reports/master-data-quality?from=2026-10-05T00:00:00Z&to=2026-10-06T00:00:00Z"
    Then the response field "coverage.measured" is "1"
    And the response field "coverage.dimensions_declared" is "1"
    And the response field "coverage.open_discrepancies" is "1"

  Scenario: A description change only makes the SKU known and counts as no registration
    Given the PUT "/products/SKU-1" with body '{"description":"Battery 12V"}' has been accepted
    And the analytics projector consumes only the analytics messages of type "ProductDescriptionChanged"
    When I GET report "/reports/master-data-quality?from=2026-10-05T00:00:00Z&to=2026-10-06T00:00:00Z"
    Then the response field "coverage.products" is "1"
    And the response field "coverage.classified" is "0"
    And the response field "days.0.registered" is "0"

  Scenario Outline: A known event missing a field the report needs is dead-lettered and applies nothing
    When the analytics projector consumes a copy of the last analytics message without its "<field>" data field
    Then 1 message is dead-lettered
    And the dead-letter reason contains "<reason>"
    When I GET report "/reports/freshness"
    Then the response field "as_of" is null

    Examples:
      | field       | reason                    |
      | version     | version is required       |
      | discrepancy | discrepancy is required   |

  Scenario: An event whose payload sku is not its subject is dead-lettered
    When the analytics projector consumes a copy of the last analytics message whose subject is "SKU-2"
    Then 1 message is dead-lettered
    And the dead-letter reason contains "does not match subject"

  Scenario: A message that is not a CloudEvent is skipped and a CloudEvent of another type is ignored
    When the analytics projector consumes a message that is not a CloudEvent
    And the analytics projector consumes a copy of the last analytics message with the type "ProductPackLevelDeclared"
    Then 0 messages are dead-lettered
    When I GET report "/reports/freshness"
    Then the response field "as_of" is null
