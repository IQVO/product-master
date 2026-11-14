# Derived from: warehouse-docs product-master (use-cases: DeclareDimensions, RecordMeasurement,
# aggregate-design-canvas: the two physical-profile state diagrams and Enforced Invariants,
# ubiquitous-language: Effective values / Discrepancy / Stale measurement, domain-events:
# ProductDimensionsDeclared and ProductMeasured payloads, troubleshooting: REST errors), ADR 0002
@bdd
Feature: Physical profile rules from the documentation
  Every edge of the NoProfile / DeclaredOnly / MeasuredOnly / DeclaredAndMeasured
  diagram, the measurement validations and what a reading replaces. The
  service clock reads 2026-10-06T15:00:00Z.

  Background:
    Given the product "SKU-1" is registered with description "Battery"

  Scenario: Declaring dimensions publishes the whole physical profile
    When I PUT "/products/SKU-1/dimensions/declared" with body '{"lengthMm":200,"widthMm":120,"heightMm":80,"weightG":1500}'
    Then the last published "ProductDimensionsDeclared" event data is '{"sku":"SKU-1","declared":{"length_mm":200,"width_mm":120,"height_mm":80,"weight_g":1500,"volume_mm3":1920000},"effective":{"length_mm":200,"width_mm":120,"height_mm":80,"weight_g":1500,"volume_mm3":1920000},"effective_source":"declared","discrepancy":false,"version":2}'

  Scenario: A measurement before any declaration is effective and has nothing to disagree with
    When I PUT "/products/SKU-1/dimensions/measured" with body '{"lengthMm":205,"widthMm":121,"heightMm":82,"weightG":1720,"measuredAt":"2026-10-06T14:05:00Z","deviceId":"CUBISCAN-03"}'
    Then the response status is 200
    And the response field "effectiveSource" is "measured"
    And the response field "declared" is absent
    And the response field "discrepancy" is "false"
    And the response field "version" is "2"
    And the last published "ProductMeasured" event data is '{"sku":"SKU-1","measured":{"length_mm":205,"width_mm":121,"height_mm":82,"weight_g":1720,"volume_mm3":2034010,"measured_at":"2026-10-06T14:05:00Z","device_id":"CUBISCAN-03"},"effective":{"length_mm":205,"width_mm":121,"height_mm":82,"weight_g":1720,"volume_mm3":2034010},"effective_source":"measured","discrepancy":false,"version":2}'

  Scenario: Declaring after a measurement computes the discrepancy and leaves the measurement effective
    Given "SKU-1" was measured at "2026-10-06T14:05:00Z"
    When I PUT "/products/SKU-1/dimensions/declared" with body '{"lengthMm":200,"widthMm":120,"heightMm":80,"weightG":1500}'
    Then the response status is 200
    And the response field "effectiveSource" is "measured"
    And the response field "effective.weightG" is "10"
    And the response field "discrepancy" is "true"
    And the response field "version" is "3"
    And the outbox event types are "ProductRegistered, ProductMeasured, ProductDimensionsDeclared"

  Scenario: A newer measurement replaces the current one and no history is kept
    Given "SKU-1" was measured at "2026-10-06T10:00:00Z"
    When I PUT "/products/SKU-1/dimensions/measured" with body '{"lengthMm":12,"widthMm":11,"heightMm":10,"weightG":15,"measuredAt":"2026-10-06T14:05:00Z"}'
    Then the response status is 200
    And the response field "measured.measuredAt" is "2026-10-06T14:05:00Z"
    And the response field "effective.weightG" is "15"
    And the response field "version" is "3"
    And the outbox event types are "ProductRegistered, ProductMeasured, ProductMeasured"

  Scenario: A different reading with the same measuredAt corrects the current one
    Given "SKU-1" was measured at "2026-10-06T14:05:00Z"
    When I record for "SKU-1" a measurement of 11 x 10 x 10 mm and 10 g taken at "2026-10-06T14:05:00Z"
    Then the response status is 200
    And the response field "effective.lengthMm" is "11"
    And the response field "version" is "3"
    And the outbox event types are "ProductRegistered, ProductMeasured, ProductMeasured"

  Scenario: Naming the device on an otherwise identical reading is a different reading
    Given "SKU-1" was measured at "2026-10-06T14:05:00Z"
    When I PUT "/products/SKU-1/dimensions/measured" with body '{"lengthMm":10,"widthMm":10,"heightMm":10,"weightG":10,"measuredAt":"2026-10-06T14:05:00Z","deviceId":"CUBISCAN-03"}'
    Then the response status is 200
    And the response field "measured.deviceId" is "CUBISCAN-03"
    And the response field "version" is "3"

  Scenario Outline: A measurement obeys the same bounds as declared dimensions
    When I PUT "/products/SKU-1/dimensions/measured" with body '<body>'
    Then the response status is 400
    And the problem type is "<slug>"
    And the outbox event types are "ProductRegistered"

    Examples:
      | body                                                                                              | slug              |
      | {"lengthMm":0,"widthMm":10,"heightMm":10,"weightG":10,"measuredAt":"2026-10-06T14:05:00Z"}       | invalid-dimension |
      | {"lengthMm":10,"widthMm":10,"heightMm":20001,"weightG":10,"measuredAt":"2026-10-06T14:05:00Z"}   | invalid-dimension |
      | {"lengthMm":10,"widthMm":10,"heightMm":10,"weightG":0,"measuredAt":"2026-10-06T14:05:00Z"}       | invalid-weight    |
      | {"lengthMm":10,"widthMm":10,"heightMm":10,"weightG":2000001,"measuredAt":"2026-10-06T14:05:00Z"} | invalid-weight    |
      | {"lengthMm":10,"widthMm":10,"weightG":10,"measuredAt":"2026-10-06T14:05:00Z"}                     | malformed-request |

  Scenario: The largest measurement the bounds allow is accepted
    When I PUT "/products/SKU-1/dimensions/measured" with body '{"lengthMm":20000,"widthMm":20000,"heightMm":20000,"weightG":2000000,"measuredAt":"2026-10-06T14:05:00Z"}'
    Then the response status is 200
    And the response field "effective.volumeMm3" is "8000000000000"

  Scenario Outline: A measurement without a usable measuredAt is a malformed request
    When I PUT "/products/SKU-1/dimensions/measured" with body '<body>'
    Then the response status is 400
    And the problem type is "malformed-request"
    And the outbox event types are "ProductRegistered"

    Examples:
      | body                                                                                  |
      | {"lengthMm":10,"widthMm":10,"heightMm":10,"weightG":10}                               |
      | {"lengthMm":10,"widthMm":10,"heightMm":10,"weightG":10,"measuredAt":null}             |
      | {"lengthMm":10,"widthMm":10,"heightMm":10,"weightG":10,"measuredAt":"yesterday"}      |
      | {"lengthMm":10,"widthMm":10,"heightMm":10,"weightG":10,"measuredAt":"2026-10-06"}     |

  Scenario Outline: A device id is at most 64 characters
    When I record for "SKU-1" a measurement taken at "2026-10-06T14:05:00Z" by a device id of <count> characters
    Then the response status is <status>
    And the outbox event types are "<events>"

    Examples:
      | count | status | events                           |
      | 64    | 200    | ProductRegistered, ProductMeasured |
      | 65    | 400    | ProductRegistered                |

  Scenario: A control character in a device id is a malformed request
    When I PUT "/products/SKU-1/dimensions/measured" with body '{"lengthMm":10,"widthMm":10,"heightMm":10,"weightG":10,"measuredAt":"2026-10-06T14:05:00Z","deviceId":"CUBI\u0007"}'
    Then the response status is 400
    And the problem type is "malformed-request"
    And the outbox event types are "ProductRegistered"

  Scenario: The volume is orientation-free, so the same box on its side is not a discrepancy
    Given "SKU-1" has declared dimensions 100 x 100 x 100 mm and 1000 g
    When I record for "SKU-1" a measurement of 200 x 50 x 100 mm and 1000 g taken at "2026-10-06T14:05:00Z"
    Then the response status is 200
    And the response field "discrepancy" is "false"
