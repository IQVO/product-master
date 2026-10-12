# Derived from: apis/openapi.yaml (declareDimensions, recordMeasurement, getPhysicalProfile),
# docs/adr/0002 (bounds, latest measurement wins, 10 % discrepancy), apis/asyncapi.yaml
# (ProductDimensionsDeclared, ProductMeasured)
@bdd
Feature: Physical profile limits and state transitions
  The edges of ADR 0002: the integer bounds, the exact 10 % discrepancy
  threshold on volume and on weight, and the order in which measurements
  win. The service clock reads 2026-10-06T15:00:00Z.

  Background:
    Given the product "SKU-1" is registered with description "Battery"

  Scenario: Declaring the same dimensions again changes nothing
    Given "SKU-1" has declared dimensions 200 x 120 x 80 mm and 1500 g
    When I PUT "/products/SKU-1/dimensions/declared" with body '{"lengthMm":200,"widthMm":120,"heightMm":80,"weightG":1500}'
    Then the response status is 200
    And the response field "version" is "2"
    And the outbox event types are "ProductRegistered, ProductDimensionsDeclared"

  Scenario Outline: The smallest and the largest dimensions are accepted
    When I PUT "/products/SKU-1/dimensions/declared" with body '<body>'
    Then the response status is 200
    And the response field "declared.volumeMm3" is "<volume>"

    Examples:
      | body                                                              | volume        |
      | {"lengthMm":1,"widthMm":1,"heightMm":1,"weightG":1}               | 1             |
      | {"lengthMm":20000,"widthMm":20000,"heightMm":20000,"weightG":2000000} | 8000000000000 |

  Scenario Outline: Zero, negative, fractional and non-numeric values are rejected
    When I PUT "/products/SKU-1/dimensions/declared" with body '<body>'
    Then the response status is 400
    And the problem type is "<slug>"
    And the outbox event types are "ProductRegistered"

    Examples:
      | body                                                      | slug              |
      | {"lengthMm":1,"widthMm":1,"heightMm":0,"weightG":1}       | invalid-dimension |
      | {"lengthMm":-5,"widthMm":1,"heightMm":1,"weightG":1}      | invalid-dimension |
      | {"lengthMm":1,"widthMm":1,"heightMm":1,"weightG":0}       | invalid-weight    |
      | {"lengthMm":1,"widthMm":1,"heightMm":1,"weightG":-1}      | invalid-weight    |
      | {"lengthMm":1.5,"widthMm":1,"heightMm":1,"weightG":1}     | malformed-request |
      | {"lengthMm":"200","widthMm":1,"heightMm":1,"weightG":1}   | malformed-request |

  Scenario Outline: The discrepancy flag turns on only beyond 10 % of the declared value
    Given "SKU-1" has declared dimensions 100 x 100 x 100 mm and 1000 g
    When I record for "SKU-1" a measurement of <length> x <width> x <height> mm and <weight> g taken at "2026-10-06T14:05:00Z"
    Then the response status is 200
    And the response field "discrepancy" is "<flag>"
    And the response field "effectiveSource" is "measured"

    Examples: Weight, exactly at and just past the threshold
      | length | width | height | weight | flag  |
      | 100    | 100   | 100    | 1100   | false |
      | 100    | 100   | 100    | 1101   | true  |
      | 100    | 100   | 100    | 900    | false |
      | 100    | 100   | 100    | 899    | true  |

    Examples: Volume, exactly at and just past the threshold
      | length | width | height | weight | flag  |
      | 110    | 100   | 100    | 1000   | false |
      | 111    | 100   | 100    | 1000   | true  |
      | 90     | 100   | 100    | 1000   | false |
      | 89     | 100   | 100    | 1000   | true  |

  Scenario Outline: A measurement may be dated up to the service clock, never after it
    When I PUT "/products/SKU-1/dimensions/measured" with body '{"lengthMm":10,"widthMm":10,"heightMm":10,"weightG":10,"measuredAt":"<measuredAt>"}'
    Then the response status is <status>
    And the outbox event types are "<events>"

    Examples:
      | measuredAt                | status | events                           |
      | 2026-10-06T15:00:00Z      | 200    | ProductRegistered, ProductMeasured |
      | 2026-10-06T12:00:00-03:00 | 200    | ProductRegistered, ProductMeasured |
      | 2026-10-06T12:30:00-03:00 | 400    | ProductRegistered                |

  Scenario: A stale measurement is judged by the instant, not by the text, and changes nothing
    Given "SKU-1" was measured at "2026-10-06T14:05:00Z"
    When I PUT "/products/SKU-1/dimensions/measured" with body '{"lengthMm":1,"widthMm":1,"heightMm":1,"weightG":1,"measuredAt":"2026-10-06T10:00:00-03:00"}'
    Then the response status is 409
    And the problem type is "stale-measurement"
    When I GET "/products/SKU-1/physical-profile"
    Then the response field "measured.measuredAt" is "2026-10-06T14:05:00Z"
    And the response field "effective.weightG" is "10"
    And the response field "version" is "2"
    And the outbox event types are "ProductRegistered, ProductMeasured"

  Scenario: Declaring again recomputes the discrepancy and keeps the measurement
    Given "SKU-1" has declared dimensions 200 x 120 x 80 mm and 1500 g
    And I PUT "/products/SKU-1/dimensions/measured" with body '{"lengthMm":205,"widthMm":121,"heightMm":82,"weightG":1720,"measuredAt":"2026-10-06T14:05:00Z","deviceId":"CUBISCAN-03"}'
    When I PUT "/products/SKU-1/dimensions/declared" with body '{"lengthMm":205,"widthMm":121,"heightMm":82,"weightG":1720}'
    Then the response status is 200
    And the response field "discrepancy" is "false"
    And the response field "effectiveSource" is "measured"
    And the response field "measured.deviceId" is "CUBISCAN-03"
    And the response field "version" is "4"
    And the outbox event types are "ProductRegistered, ProductDimensionsDeclared, ProductMeasured, ProductDimensionsDeclared"

  Scenario: A measurement publishes the whole physical profile
    Given "SKU-1" has declared dimensions 200 x 120 x 80 mm and 1500 g
    When I PUT "/products/SKU-1/dimensions/measured" with body '{"lengthMm":205,"widthMm":121,"heightMm":82,"weightG":1720,"measuredAt":"2026-10-06T14:05:00Z","deviceId":"CUBISCAN-03"}'
    Then the last published "ProductMeasured" event data is '{"sku":"SKU-1","declared":{"length_mm":200,"width_mm":120,"height_mm":80,"weight_g":1500,"volume_mm3":1920000},"measured":{"length_mm":205,"width_mm":121,"height_mm":82,"weight_g":1720,"volume_mm3":2034010,"measured_at":"2026-10-06T14:05:00Z","device_id":"CUBISCAN-03"},"effective":{"length_mm":205,"width_mm":121,"height_mm":82,"weight_g":1720,"volume_mm3":2034010},"effective_source":"measured","discrepancy":true,"version":3}'
