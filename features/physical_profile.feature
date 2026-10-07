Feature: Declared versus measured physical profile
  Declared dimensions come from a vendor or steward; a measurement (cubiscan
  or manual) becomes the effective value. A discrepancy is flagged when the
  measured volume or weight differs from the declared one by more than 10 %
  (ADR 0002). The service clock reads 2026-10-06T15:00:00Z.

  Background:
    Given the product "SKU-1" is registered with description "Battery"

  Scenario: Declared dimensions are effective until a measurement exists
    When I PUT "/products/SKU-1/dimensions/declared" with body '{"lengthMm":200,"widthMm":120,"heightMm":80,"weightG":1500}'
    Then the response status is 200
    And the response field "declared.volumeMm3" is "1920000"
    And the response field "effectiveSource" is "declared"
    And the response field "effective.weightG" is "1500"
    And the response field "discrepancy" is "false"
    And the outbox event types are "ProductRegistered, ProductDimensionsDeclared"

  Scenario: A measurement far from the declaration becomes effective and is flagged
    Given "SKU-1" has declared dimensions 200 x 120 x 80 mm and 1500 g
    When I PUT "/products/SKU-1/dimensions/measured" with body '{"lengthMm":205,"widthMm":121,"heightMm":82,"weightG":1720,"measuredAt":"2026-10-06T14:05:00Z","deviceId":"CUBISCAN-03"}'
    Then the response status is 200
    And the response field "effectiveSource" is "measured"
    And the response field "effective.weightG" is "1720"
    And the response field "measured.deviceId" is "CUBISCAN-03"
    And the response field "discrepancy" is "true"
    And the response field "version" is "3"
    And the outbox event types are "ProductRegistered, ProductDimensionsDeclared, ProductMeasured"

  Scenario: A measurement within 10 % is not a discrepancy
    Given "SKU-1" has declared dimensions 200 x 120 x 80 mm and 1500 g
    When I PUT "/products/SKU-1/dimensions/measured" with body '{"lengthMm":201,"widthMm":120,"heightMm":80,"weightG":1600,"measuredAt":"2026-10-06T14:05:00Z"}'
    Then the response status is 200
    And the response field "discrepancy" is "false"
    And the response field "measured.deviceId" is absent

  Scenario: An older measurement than the current one is stale
    Given "SKU-1" was measured at "2026-10-06T14:05:00Z"
    When I PUT "/products/SKU-1/dimensions/measured" with body '{"lengthMm":1,"widthMm":1,"heightMm":1,"weightG":1,"measuredAt":"2026-10-06T13:00:00Z"}'
    Then the response status is 409
    And the problem type is "stale-measurement"

  Scenario: A measurement dated in the future is rejected
    When I PUT "/products/SKU-1/dimensions/measured" with body '{"lengthMm":1,"widthMm":1,"heightMm":1,"weightG":1,"measuredAt":"2026-10-06T15:00:01Z"}'
    Then the response status is 400
    And the problem type is "measured-at-in-future"

  Scenario: Repeating the current measurement changes nothing
    Given "SKU-1" was measured at "2026-10-06T14:05:00Z"
    When I PUT "/products/SKU-1/dimensions/measured" with body '{"lengthMm":10,"widthMm":10,"heightMm":10,"weightG":10,"measuredAt":"2026-10-06T14:05:00Z"}'
    Then the response status is 200
    And the response field "version" is "2"

  Scenario Outline: Out-of-range dimensions are rejected
    When I PUT "/products/SKU-1/dimensions/declared" with body '<body>'
    Then the response status is 400
    And the problem type is "<slug>"

    Examples:
      | body                                                     | slug              |
      | {"lengthMm":0,"widthMm":1,"heightMm":1,"weightG":1}      | invalid-dimension |
      | {"lengthMm":1,"widthMm":20001,"heightMm":1,"weightG":1}  | invalid-dimension |
      | {"lengthMm":1,"widthMm":1,"heightMm":1,"weightG":2000001} | invalid-weight    |
      | {"lengthMm":1,"widthMm":1,"heightMm":1}                  | malformed-request |

  Scenario: The physical profile of a product with nothing known
    When I GET "/products/SKU-1/physical-profile"
    Then the response status is 200
    And the response field "effectiveSource" is "none"
    And the response field "effective" is absent
