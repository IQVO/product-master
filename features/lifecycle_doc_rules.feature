# Derived from: warehouse-docs product-master (domain-events: common attributes and ordering/replay, aggregate
# canvas: Created Events and "Each command raises at most one event", use-cases: RetirePackLevel/DeclarePackLevel/
# RecordPackLevelMeasurement, troubleshooting: pack-quantity-immutable, pack-level-order, "no implicit registration",
# eventstorming hotspot "No unclassify command or event in v1")
@bdd
Feature: Lifecycle rules of the product across its commands
  Every accepted change raises its own CloudEvent and adds one to the version, a change that alters nothing
  raises nothing, a SKU that was never registered gains nothing from a rejected write, and v1 offers no way to
  delete or unclassify. The service clock reads 2026-10-06T15:00:00Z.

  Background:
    Given the product "SKU-1" is registered with description "Battery"

  Scenario: Every event type carries its own CloudEvents type, subject and data schema
    When I PUT "/products/SKU-1" with body '{"description":"Battery AA"}'
    Then the last outbox message is a "ProductDescriptionChanged" CloudEvent about "SKU-1" with data schema "urn:warehouse:product-master:events:ProductDescriptionChanged:v1"
    When I PUT "/products/SKU-1/classification" with body '{"handlingTags":["Fragile"]}'
    Then the last outbox message is a "ProductClassified" CloudEvent about "SKU-1" with data schema "urn:warehouse:product-master:events:ProductClassified:v1"
    When I PUT "/products/SKU-1/dimensions/declared" with body '{"lengthMm":200,"widthMm":120,"heightMm":80,"weightG":1500}'
    Then the last outbox message is a "ProductDimensionsDeclared" CloudEvent about "SKU-1" with data schema "urn:warehouse:product-master:events:ProductDimensionsDeclared:v1"
    When I PUT "/products/SKU-1/dimensions/measured" with body '{"lengthMm":205,"widthMm":121,"heightMm":82,"weightG":1720,"measuredAt":"2026-10-06T14:05:00Z"}'
    Then the last outbox message is a "ProductMeasured" CloudEvent about "SKU-1" with data schema "urn:warehouse:product-master:events:ProductMeasured:v1"
    When I PUT "/products/SKU-1/pack-levels/EACH" with body '{"gtin":"4006381333931"}'
    Then the last outbox message is a "ProductPackLevelDeclared" CloudEvent about "SKU-1" with data schema "urn:warehouse:product-master:events:ProductPackLevelDeclared:v1"
    When I PUT "/products/SKU-1/pack-levels/EACH/measured" with body '{"lengthMm":205,"widthMm":121,"heightMm":82,"weightG":1720,"measuredAt":"2026-10-06T14:05:00Z"}'
    Then the last outbox message is a "ProductPackLevelMeasured" CloudEvent about "SKU-1" with data schema "urn:warehouse:product-master:events:ProductPackLevelMeasured:v1"
    When I POST "/products/SKU-1/pack-levels/EACH/retirements" with body '{"reason":"relabelled"}'
    Then the last outbox message is a "ProductPackLevelRetired" CloudEvent about "SKU-1" with data schema "urn:warehouse:product-master:events:ProductPackLevelRetired:v1"

  Scenario: Each accepted change adds one to the version, a repeated command adds nothing and a supersede adds two
    Given the PUT "/products/SKU-1/classification" with body '{"handlingTags":["Fragile"]}' has been accepted
    And the PUT "/products/SKU-1/dimensions/declared" with body '{"lengthMm":200,"widthMm":120,"heightMm":80,"weightG":1500}' has been accepted
    And the PUT "/products/SKU-1/dimensions/measured" with body '{"lengthMm":205,"widthMm":121,"heightMm":82,"weightG":1720,"measuredAt":"2026-10-06T14:05:00Z"}' has been accepted
    And "SKU-1" has the each GTIN "4006381333931"
    And "SKU-1" has a case of 12 eaches with GTIN "24006381333935"
    When I PUT "/products/SKU-1" with body '{"description":"Battery"}'
    And I PUT "/products/SKU-1/classification" with body '{"handlingTags":["Fragile"]}'
    And I PUT "/products/SKU-1/dimensions/declared" with body '{"lengthMm":200,"widthMm":120,"heightMm":80,"weightG":1500}'
    And I PUT "/products/SKU-1/dimensions/measured" with body '{"lengthMm":205,"widthMm":121,"heightMm":82,"weightG":1720,"measuredAt":"2026-10-06T14:05:00Z"}'
    And I PUT "/products/SKU-1/pack-levels/CASE" with body '{"gtin":"24006381333935","childGtin":"4006381333931","qtyOfChild":12,"isOrderable":true,"isDespatchUnit":true}'
    Then the outbox event versions are "1,2,3,4,5,6"
    When I PUT "/products/SKU-1/pack-levels/CASE" with body '{"gtin":"34006381333932","childGtin":"4006381333931","qtyOfChild":24}'
    Then the outbox event versions are "1,2,3,4,5,6,7,8"
    And the outbox event types end with "ProductPackLevelRetired, ProductPackLevelDeclared"

  Scenario: A measurement without a device id is published without a device_id
    When I PUT "/products/SKU-1/dimensions/measured" with body '{"lengthMm":205,"widthMm":121,"heightMm":82,"weightG":1720,"measuredAt":"2026-10-06T14:05:00Z"}'
    Then the response status is 200
    And the response field "measured.deviceId" is absent
    And the last outbox message data has no field "device_id"
    Given "SKU-1" has the each GTIN "4006381333931"
    When I PUT "/products/SKU-1/pack-levels/EACH/measured" with body '{"lengthMm":205,"widthMm":121,"heightMm":82,"weightG":1720,"measuredAt":"2026-10-06T14:05:00Z"}'
    Then the response status is 200
    And the last outbox message data has no field "device_id"

  Scenario: Retiring a level that is already retired changes nothing
    Given "SKU-1" has the each GTIN "4006381333931"
    And "SKU-1" has a case of 12 eaches with GTIN "24006381333935"
    And I POST "/products/SKU-1/pack-levels/CASE/retirements" with body '{"reason":"discontinued"}' and the Idempotency-Key "retire-case-first"
    And the service clock reads "2026-10-07T09:00:00Z"
    When I POST "/products/SKU-1/pack-levels/CASE/retirements" with body '{"reason":"relabelled"}' and the Idempotency-Key "retire-case-second"
    Then the response status is 200
    And the response field "active" is "false"
    And the response field "effectiveTo" is "2026-10-06T15:00:00Z"
    And the outbox holds 4 messages
    And the outbox event versions are "1,2,3,4"

  Scenario: Superseding the each does not move the case above it: the case is declared again with a new GTIN
    Given "SKU-1" has the each GTIN "4006381333931"
    And "SKU-1" has a case of 12 eaches with GTIN "24006381333935"
    When I PUT "/products/SKU-1/pack-levels/EACH" with body '{"gtin":"5901234123457"}'
    Then the response status is 200
    And the outbox event types end with "ProductPackLevelRetired, ProductPackLevelDeclared"
    When I PUT "/products/SKU-1/pack-levels/CASE" with body '{"gtin":"24006381333935","childGtin":"5901234123457","qtyOfChild":12,"isOrderable":true,"isDespatchUnit":true}'
    Then the response status is 409
    And the problem type is "pack-quantity-immutable"
    When I PUT "/products/SKU-1/pack-levels/CASE" with body '{"gtin":"34006381333932","childGtin":"5901234123457","qtyOfChild":12}'
    Then the response status is 200
    And the response field "childGtin" is "05901234123457"
    And the outbox event types end with "ProductPackLevelRetired, ProductPackLevelDeclared"

  Scenario: The child of a level is a level of the same product, not of another SKU
    Given "SKU-1" has the each GTIN "4006381333931"
    And the product "SKU-2" is registered with description "Charger"
    When I PUT "/products/SKU-2/pack-levels/CASE" with body '{"gtin":"24006381333935","childGtin":"4006381333931","qtyOfChild":12}'
    Then the response status is 409
    And the problem type is "pack-level-order"
    And the outbox holds 3 messages

  Scenario Outline: A pack level carries the bounds of unit dimensions: 1 to 20000 mm and 1 to 2000000 g
    Given "SKU-1" has the each GTIN "4006381333931"
    When I PUT "/products/SKU-1/pack-levels/CASE" with body '{"gtin":"24006381333935","childGtin":"4006381333931","qtyOfChild":12,"dims":{"lengthMm":<length>,"widthMm":<width>,"heightMm":<height>,"weightG":<weight>}}'
    Then the response status is <status>
    And the outbox holds <messages> messages

    Examples:
      | length | width | height | weight  | status | messages |
      | 20000  | 20000 | 20000  | 2000000 | 201    | 3        |
      | 20001  | 300   | 200    | 6000    | 400    | 2        |
      | 400    | 0     | 200    | 6000    | 400    | 2        |
      | 400    | 300   | 20001  | 6000    | 400    | 2        |
      | 400    | 300   | 200    | 2000001 | 400    | 2        |
      | 400    | 300   | 200    | 0       | 400    | 2        |

  Scenario: Measuring a pack level leaves the physical profile of the unit alone
    Given "SKU-1" has the each GTIN "4006381333931"
    And "SKU-1" has a case of 12 eaches with GTIN "24006381333935"
    When I PUT "/products/SKU-1/pack-levels/CASE/measured" with body '{"lengthMm":410,"widthMm":310,"heightMm":210,"weightG":6900,"measuredAt":"2026-10-06T14:05:00Z"}'
    Then the response status is 200
    When I GET "/products/SKU-1/physical-profile"
    Then the response field "effectiveSource" is "none"
    And the response field "effective" is absent
    And the response field "measured" is absent

  Scenario: A registered product without a pack level lists an empty collection
    When I GET "/products/SKU-1/pack-levels"
    Then the response status is 200
    And the response list "items" has 0 items
    And there is no next page

  Scenario: A rejected write to a SKU that was never registered registers nothing
    When I PUT "/products/SKU-404/classification" with body '{"handlingTags":["Fragile"]}'
    Then the response status is 404
    When I PUT "/products/SKU-404/dimensions/declared" with body '{"lengthMm":200,"widthMm":120,"heightMm":80,"weightG":1500}'
    Then the response status is 404
    When I PUT "/products/SKU-404/pack-levels/EACH" with body '{"gtin":"4006381333931"}'
    Then the response status is 404
    And the problem type is "product-not-found"
    When I GET "/products/SKU-404"
    Then the response status is 404
    When I list products with query ""
    Then the listed SKUs are "SKU-1"
    And the outbox holds 1 messages

  Scenario Outline: Version 1 has no delete and no unclassify
    Given "SKU-1" is classified with tags "Fragile"
    When I send DELETE "<path>"
    Then the response status is a client error
    When I GET "<read>"
    Then the response status is 200

    Examples:
      | path                           | read                           |
      | /products/SKU-1                | /products/SKU-1                |
      | /products/SKU-1/classification | /products/SKU-1/classification |

  Scenario: A SKU is case-sensitive: sku-1 is another product than SKU-1
    When I PUT "/products/sku-1" with body '{"description":"Charger"}'
    Then the response status is 201
    And the response field "version" is "1"
    When I GET "/products/SKU-1"
    Then the response field "description" is "Battery"
    When I list products with query ""
    Then the listed SKUs are "SKU-1,sku-1"
