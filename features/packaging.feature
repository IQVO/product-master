# Derived from: apis/openapi.yaml (declarePackLevel, retirePackLevel, recordPackLevelMeasurement,
# listPackLevels, resolveGtin), docs/adr/0011 (GS1 immutability, GTIN uniqueness, indicator digit 9,
# the lower child level), apis/asyncapi.yaml (ProductPackLevelDeclared, Retired, Measured)
@bdd
Feature: Packaging hierarchy with GS1 GTINs
  A product is sold, shipped and received in pack levels (EACH, INNER, CASE, PALLET), each
  identified by a GTIN. Eaches are the ledger unit: unitsPerPack is the factor to eaches.
  Once a level is active its content quantity never changes: a new quantity is a new GTIN at
  that level and the old level is end-dated, never edited. The service clock reads
  2026-10-06T15:00:00Z. Fixture GTINs: each 4006381333931 (EAN-13), cases 24006381333935 and
  34006381333932, pallet 54006381333936, variable measure 94006381333934, other EAN 5901234123457.

  Background:
    Given the product "SKU-1" is registered with description "Battery"
    And "SKU-1" has the each GTIN "4006381333931"

  Scenario: Declaring a case of 12 eaches
    When I PUT "/products/SKU-1/pack-levels/CASE" with body '{"gtin":"24006381333935","childGtin":"4006381333931","qtyOfChild":12,"isOrderable":true,"isDespatchUnit":true}'
    Then the response status is 201
    And the response field "level" is "CASE"
    And the response field "gtin" is "24006381333935"
    And the response field "childGtin" is "04006381333931"
    And the response field "qtyOfChild" is "12"
    And the response field "unitsPerPack" is "12"
    And the response field "active" is "true"
    And the response field "version" is "3"
    And the outbox event types are "ProductRegistered, ProductPackLevelDeclared, ProductPackLevelDeclared"
    And the last published "ProductPackLevelDeclared" event data is '{"sku":"SKU-1","level":"CASE","gtin":"24006381333935","child_gtin":"04006381333931","qty_of_child":12,"units_per_pack":12,"is_orderable":true,"is_despatch_unit":true,"effective_from":"2026-10-06T15:00:00Z","version":3}'
    And the last outbox message is a CloudEvent on topic "warehouse.product-master.events" keyed "SKU-1" about "SKU-1" at "2026-10-06T15:00:00Z" with data schema "urn:warehouse:product-master:events:ProductPackLevelDeclared:v1"

  Scenario: Declaring the same level again changes nothing
    Given "SKU-1" has a case of 12 eaches with GTIN "24006381333935"
    When I PUT "/products/SKU-1/pack-levels/CASE" with body '{"gtin":"24006381333935","childGtin":"4006381333931","qtyOfChild":12,"isOrderable":true,"isDespatchUnit":true}'
    Then the response status is 200
    And the response field "version" is "3"
    And the outbox holds 3 messages

  Scenario: A quantity change on an active level is rejected
    Given "SKU-1" has a case of 12 eaches with GTIN "24006381333935"
    When I PUT "/products/SKU-1/pack-levels/CASE" with body '{"gtin":"24006381333935","childGtin":"4006381333931","qtyOfChild":24,"isOrderable":true,"isDespatchUnit":true}'
    Then the response status is 409
    And the problem type is "pack-quantity-immutable"
    And the outbox holds 3 messages
    When I GET "/gtins/24006381333935"
    Then the response field "unitsPerPack" is "12"
    And the response field "active" is "true"

  Scenario: A new GTIN at that level is accepted and the old level is end-dated
    Given "SKU-1" has a case of 12 eaches with GTIN "24006381333935"
    When I PUT "/products/SKU-1/pack-levels/CASE" with body '{"gtin":"34006381333932","childGtin":"4006381333931","qtyOfChild":24,"isDespatchUnit":true}'
    Then the response status is 200
    And the response field "gtin" is "34006381333932"
    And the response field "unitsPerPack" is "24"
    And the response field "version" is "5"
    And the outbox event types end with "ProductPackLevelRetired, ProductPackLevelDeclared"
    And the last published "ProductPackLevelRetired" event data is '{"sku":"SKU-1","level":"CASE","gtin":"24006381333935","effective_to":"2026-10-06T15:00:00Z","reason":"superseded","version":4}'
    When I GET "/gtins/24006381333935"
    Then the response status is 200
    And the response field "active" is "false"
    And the response field "effectiveTo" is "2026-10-06T15:00:00Z"
    And the response field "unitsPerPack" is "12"
    When I GET "/gtins/34006381333932"
    Then the response field "active" is "true"
    And the response field "unitsPerPack" is "24"
    When I GET "/products/SKU-1/pack-levels"
    Then the response status is 200
    And the response field "version" is "5"

  Scenario: A GTIN already assigned to another SKU is rejected
    Given the product "SKU-2" is registered with description "Charger"
    When I PUT "/products/SKU-2/pack-levels/EACH" with body '{"gtin":"4006381333931"}'
    Then the response status is 409
    And the problem type is "gtin-already-assigned"
    And the outbox holds 3 messages

  Scenario: The same GTIN in another spelling is the same GTIN
    Given the product "SKU-2" is registered with description "Charger"
    When I PUT "/products/SKU-2/pack-levels/EACH" with body '{"gtin":"04006381333931"}'
    Then the response status is 409
    And the problem type is "gtin-already-assigned"

  Scenario: A retired GTIN is never reassigned
    Given the product "SKU-2" is registered with description "Charger"
    When I POST "/products/SKU-1/pack-levels/EACH:retire" with body '{"reason":"relabelled"}'
    Then the response status is 200
    When I PUT "/products/SKU-2/pack-levels/EACH" with body '{"gtin":"4006381333931"}'
    Then the response status is 409
    And the problem type is "gtin-already-assigned"

  Scenario Outline: A GTIN with a wrong length or check digit is rejected
    When I PUT "/products/SKU-1/pack-levels/PALLET" with body '{"gtin":"<gtin>","childGtin":"4006381333931","qtyOfChild":100}'
    Then the response status is 400
    And the problem type is "invalid-gtin"
    And the outbox holds 2 messages

    Examples:
      | gtin           |
      | 54006381333937 |
      | 5400638133393  |
      | 5400638133393x |
      | 12345          |
      |                |

  Scenario: A variable measure GTIN (indicator digit 9) is rejected
    When I PUT "/products/SKU-1/pack-levels/CASE" with body '{"gtin":"94006381333934","childGtin":"4006381333931","qtyOfChild":12}'
    Then the response status is 400
    And the problem type is "variable-measure-unsupported"
    And the outbox holds 2 messages

  Scenario: A case needs a lower child level
    When I PUT "/products/SKU-1/pack-levels/CASE" with body '{"gtin":"24006381333935","qtyOfChild":12}'
    Then the response status is 409
    And the problem type is "pack-level-order"
    When I PUT "/products/SKU-1/pack-levels/CASE" with body '{"gtin":"24006381333935","childGtin":"5901234123457","qtyOfChild":12}'
    Then the response status is 409
    And the problem type is "pack-level-order"
    And the outbox holds 2 messages

  Scenario: A child must be lower than its parent
    Given "SKU-1" has a case of 12 eaches with GTIN "24006381333935"
    When I PUT "/products/SKU-1/pack-levels/INNER" with body '{"gtin":"14006381333938","childGtin":"24006381333935","qtyOfChild":2}'
    Then the response status is 409
    And the problem type is "pack-level-order"

  Scenario Outline: A pack quantity must make a real pack
    When I PUT "/products/SKU-1/pack-levels/CASE" with body '<body>'
    Then the response status is 400
    And the problem type is "invalid-pack-quantity"

    Examples:
      | body                                                                          |
      | {"gtin":"24006381333935","childGtin":"4006381333931","qtyOfChild":1}          |
      | {"gtin":"24006381333935","childGtin":"4006381333931","qtyOfChild":0}          |
      | {"gtin":"24006381333935","childGtin":"4006381333931","qtyOfChild":1000001}    |
      | {"gtin":"24006381333935","childGtin":"4006381333931","qtyOfChild":12,"unitsPerPack":10} |

  Scenario: A pallet of cases multiplies the direct factors
    Given "SKU-1" has a case of 12 eaches with GTIN "24006381333935"
    When I PUT "/products/SKU-1/pack-levels/PALLET" with body '{"gtin":"54006381333936","childGtin":"24006381333935","qtyOfChild":50}'
    Then the response status is 201
    And the response field "unitsPerPack" is "600"

  Scenario: A case cannot weigh less than its contents
    Given "SKU-1" has declared dimensions 100 x 50 x 40 mm and 500 g
    When I PUT "/products/SKU-1/pack-levels/CASE" with body '{"gtin":"24006381333935","childGtin":"4006381333931","qtyOfChild":12,"dims":{"lengthMm":400,"widthMm":300,"heightMm":200,"weightG":5999}}'
    Then the response status is 409
    And the problem type is "pack-dimensions-below-contents"
    And the outbox holds 3 messages

  Scenario: Retiring a level keeps its GTIN resolvable
    Given "SKU-1" has a case of 12 eaches with GTIN "24006381333935"
    When I POST "/products/SKU-1/pack-levels/CASE:retire" with body '{"reason":"discontinued"}'
    Then the response status is 200
    And the response field "active" is "false"
    And the response field "version" is "4"
    And the last published "ProductPackLevelRetired" event data is '{"sku":"SKU-1","level":"CASE","gtin":"24006381333935","effective_to":"2026-10-06T15:00:00Z","reason":"discontinued","version":4}'
    When I POST "/products/SKU-1/pack-levels/CASE:retire"
    Then the response status is 200
    And the response field "version" is "4"
    And the outbox holds 4 messages
    When I GET "/gtins/24006381333935"
    Then the response field "active" is "false"

  Scenario: Retiring a level the SKU never had
    When I POST "/products/SKU-1/pack-levels/PALLET:retire"
    Then the response status is 404
    And the problem type is "pack-level-not-found"

  Scenario: Measuring a pack level
    Given "SKU-1" has a case of 12 eaches with GTIN "24006381333935"
    When I PUT "/products/SKU-1/pack-levels/CASE/measured" with body '{"lengthMm":410,"widthMm":310,"heightMm":210,"weightG":6900,"measuredAt":"2026-10-06T14:05:00Z","deviceId":"CUBISCAN-03"}'
    Then the response status is 200
    And the response field "measured.weightG" is "6900"
    And the response field "measured.deviceId" is "CUBISCAN-03"
    And the last published "ProductPackLevelMeasured" event data is '{"sku":"SKU-1","level":"CASE","gtin":"24006381333935","dims":{"length_mm":410,"width_mm":310,"height_mm":210,"weight_g":6900},"measured_at":"2026-10-06T14:05:00Z","device_id":"CUBISCAN-03","version":4}'

  Scenario: Resolving a GTIN from any accepted spelling
    When I GET "/gtins/4006381333931"
    Then the response status is 200
    And the response field "sku" is "SKU-1"
    And the response field "level" is "EACH"
    And the response field "unitsPerPack" is "1"
    And the response field "gtin" is "04006381333931"
    When I GET "/gtins/5901234123457"
    Then the response status is 404
    And the problem type is "gtin-not-found"
    When I GET "/gtins/4006381333930"
    Then the response status is 400
    And the problem type is "invalid-gtin"
