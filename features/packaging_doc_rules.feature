# Derived from: warehouse-docs product-master (use-cases: DeclarePackLevel / RetirePackLevel /
# RecordPackLevelMeasurement / ResolveGTIN and the invariants table, aggregate-design-canvas:
# PackagingHierarchy invariants, ubiquitous-language: GTIN / Units per pack / Supersede / Retire,
# domain-events: pack level payloads, troubleshooting: REST errors), ADR 0011
@bdd
Feature: Packaging rules from the documentation
  GS1 identity (a GTIN identifies one level for ever), the chain of direct
  factors to eaches, the weight of a pack against its contents, and the
  life of a level from declaration to retirement. Fixture GTINs: each
  4006381333931, inner 14006381333938, cases 24006381333935 / 34006381333932,
  pallet 54006381333936, other EAN 5901234123457. The service clock reads
  2026-10-06T15:00:00Z.

  Background:
    Given the product "SKU-1" is registered with description "Battery"
    And "SKU-1" has the each GTIN "4006381333931"

  Scenario: An each level is published without a child and with one unit per pack
    Then the last published "ProductPackLevelDeclared" event data is '{"sku":"SKU-1","level":"EACH","gtin":"04006381333931","units_per_pack":1,"is_orderable":true,"is_despatch_unit":false,"effective_from":"2026-10-06T15:00:00Z","version":2}'

  Scenario Outline: A GTIN of 8, 12, 13 or 14 digits is accepted and normalised to 14 digits
    Given the product "SKU-2" is registered with description "Charger"
    When I PUT "/products/SKU-2/pack-levels/EACH" with body '{"gtin":"<gtin>"}'
    Then the response status is 201
    And the response field "gtin" is "<normalised>"
    When I GET "/gtins/<gtin>"
    Then the response status is 200
    And the response field "sku" is "SKU-2"
    And the response field "gtin" is "<normalised>"

    Examples:
      | gtin           | normalised     |
      | 96385074       | 00000096385074 |
      | 036000291452   | 00036000291452 |
      | 5901234123457  | 05901234123457 |
      | 10614141000415 | 10614141000415 |

  Scenario Outline: A GTIN that is not 8, 12, 13 or 14 digits with a valid check digit cannot be resolved
    When I GET "/gtins/<gtin>"
    Then the response status is 400
    And the problem type is "<slug>"

    Examples:
      | gtin            | slug                         |
      | 00000000000000  | invalid-gtin                 |
      | 1234567890      | invalid-gtin                 |
      | 123456789012345 | invalid-gtin                 |
      | 4006-381333931  | invalid-gtin                 |
      | 94006381333934  | variable-measure-unsupported |

  Scenario Outline: A pack level is one of the exact upper-case names EACH, INNER, CASE or PALLET
    When I send <method> "<path>" with body '<body>'
    Then the response status is 400
    And the problem type is "invalid-pack-level"
    And the outbox holds 2 messages

    Examples:
      | method | path                                          | body                                                                    |
      | PUT    | /products/SKU-1/pack-levels/case              | {"gtin":"24006381333935","childGtin":"4006381333931","qtyOfChild":12}   |
      | PUT    | /products/SKU-1/pack-levels/Each              | {"gtin":"5901234123457"}                                                |
      | PUT    | /products/SKU-1/pack-levels/PACK              | {"gtin":"5901234123457"}                                                |
      | POST   | /products/SKU-1/pack-levels/pallet/retirements | {}                                                                      |
      | PUT    | /products/SKU-1/pack-levels/inner/measured    | {"lengthMm":10,"widthMm":10,"heightMm":10,"weightG":10,"measuredAt":"2026-10-06T14:05:00Z"} |

  Scenario Outline: Every pack level operation on an unregistered SKU is a 404
    When I send <method> "<path>" with body '<body>'
    Then the response status is 404
    And the problem type is "product-not-found"

    Examples:
      | method | path                                           | body                                                                                        |
      | PUT    | /products/SKU-404/pack-levels/EACH             | {"gtin":"5901234123457"}                                                                    |
      | POST   | /products/SKU-404/pack-levels/EACH/retirements | {}                                                                                          |
      | PUT    | /products/SKU-404/pack-levels/EACH/measured    | {"lengthMm":10,"widthMm":10,"heightMm":10,"weightG":10,"measuredAt":"2026-10-06T14:05:00Z"} |
      | GET    | /products/SKU-404/pack-levels                  |                                                                                             |

  Scenario Outline: An each level has no child and exactly one unit per pack
    Given the product "SKU-2" is registered with description "Charger"
    When I PUT "/products/SKU-2/pack-levels/EACH" with body '<body>'
    Then the response status is 400
    And the problem type is "invalid-pack-quantity"
    And the outbox holds 3 messages

    Examples:
      | body                                                      |
      | {"gtin":"5901234123457","qtyOfChild":2}                   |
      | {"gtin":"5901234123457","childGtin":"4006381333931"}      |
      | {"gtin":"5901234123457","unitsPerPack":2}                 |

  Scenario Outline: A pack holds from 2 up to 1000000 of its child
    When I PUT "/products/SKU-1/pack-levels/CASE" with body '{"gtin":"24006381333935","childGtin":"4006381333931","qtyOfChild":<qty>}'
    Then the response status is 201
    And the response field "qtyOfChild" is "<qty>"
    And the response field "unitsPerPack" is "<qty>"

    Examples:
      | qty     |
      | 2       |
      | 1000000 |

  Scenario Outline: The units per pack of a pallet are the product of the direct factors, at most 1000000
    Given "SKU-1" has a case of 1000 eaches with GTIN "24006381333935"
    When I PUT "/products/SKU-1/pack-levels/PALLET" with body '{"gtin":"54006381333936","childGtin":"24006381333935","qtyOfChild":<cases>}'
    Then the response status is <status>
    And the outbox holds <messages> messages

    Examples:
      | cases | status | messages |
      | 1000  | 201    | 4        |
      | 1001  | 400    | 3        |

  Scenario Outline: A stated units per pack must equal the chain
    Given "SKU-1" has a case of 12 eaches with GTIN "24006381333935"
    When I PUT "/products/SKU-1/pack-levels/PALLET" with body '{"gtin":"54006381333936","childGtin":"24006381333935","qtyOfChild":50,"unitsPerPack":<units>}'
    Then the response status is <status>

    Examples:
      | units | status |
      | 600   | 201    |
      | 601   | 400    |

  Scenario: Packs of packs chain through the inner level
    Given the PUT "/products/SKU-1/pack-levels/INNER" with body '{"gtin":"14006381333938","childGtin":"4006381333931","qtyOfChild":6}' has been accepted
    When I PUT "/products/SKU-1/pack-levels/CASE" with body '{"gtin":"24006381333935","childGtin":"14006381333938","qtyOfChild":4}'
    Then the response status is 201
    And the response field "unitsPerPack" is "24"
    And the response field "childGtin" is "14006381333938"
    When I PUT "/products/SKU-1/pack-levels/PALLET" with body '{"gtin":"54006381333936","childGtin":"24006381333935","qtyOfChild":10}'
    Then the response field "unitsPerPack" is "240"

  Scenario: A GTIN identifies one level for ever: not two levels of a SKU and not again once superseded
    Given "SKU-1" has a case of 12 eaches with GTIN "24006381333935"
    When I PUT "/products/SKU-1/pack-levels/INNER" with body '{"gtin":"24006381333935","childGtin":"4006381333931","qtyOfChild":6}'
    Then the response status is 409
    And the problem type is "gtin-already-assigned"
    Given the PUT "/products/SKU-1/pack-levels/CASE" with body '{"gtin":"34006381333932","childGtin":"4006381333931","qtyOfChild":24}' has been accepted
    When I PUT "/products/SKU-1/pack-levels/CASE" with body '{"gtin":"24006381333935","childGtin":"4006381333931","qtyOfChild":12}'
    Then the response status is 409
    And the problem type is "gtin-already-assigned"
    And the outbox holds 5 messages
    When I GET "/gtins/24006381333935"
    Then the response field "active" is "false"

  Scenario Outline: Under an existing GTIN neither the child, the quantity nor the units per pack can change
    Given the PUT "/products/SKU-1/pack-levels/INNER" with body '{"gtin":"14006381333938","childGtin":"4006381333931","qtyOfChild":6}' has been accepted
    And "SKU-1" has a case of 12 eaches with GTIN "24006381333935"
    When I PUT "/products/SKU-1/pack-levels/CASE" with body '<body>'
    Then the response status is 409
    And the problem type is "pack-quantity-immutable"
    And the outbox holds 4 messages

    Examples:
      | body                                                                                              |
      | {"gtin":"24006381333935","childGtin":"14006381333938","qtyOfChild":2}                             |
      | {"gtin":"24006381333935","childGtin":"4006381333931","qtyOfChild":12,"unitsPerPack":24}           |

  Scenario: Changing the flags of an active level is a new declaration with the same effective_from
    Given "SKU-1" has a case of 12 eaches with GTIN "24006381333935"
    When I PUT "/products/SKU-1/pack-levels/CASE" with body '{"gtin":"24006381333935","childGtin":"4006381333931","qtyOfChild":12,"isOrderable":false,"isDespatchUnit":true}'
    Then the response status is 200
    And the response field "version" is "4"
    And the last published "ProductPackLevelDeclared" event data is '{"sku":"SKU-1","level":"CASE","gtin":"24006381333935","child_gtin":"04006381333931","qty_of_child":12,"units_per_pack":12,"is_orderable":false,"is_despatch_unit":true,"effective_from":"2026-10-06T15:00:00Z","version":4}'
    When I GET "/products/SKU-1/pack-levels"
    Then the response field "items.1.gtin" is "24006381333935"
    And the response field "items.1.isOrderable" is "false"

  Scenario: Adding dimensions to an active level is a new declaration, and the same dimensions again change nothing
    Given "SKU-1" has a case of 12 eaches with GTIN "24006381333935"
    When I PUT "/products/SKU-1/pack-levels/CASE" with body '{"gtin":"24006381333935","childGtin":"4006381333931","qtyOfChild":12,"isOrderable":true,"isDespatchUnit":true,"dims":{"lengthMm":400,"widthMm":300,"heightMm":200,"weightG":6000}}'
    Then the response status is 200
    And the response field "version" is "4"
    And the response field "dims.weightG" is "6000"
    When I PUT "/products/SKU-1/pack-levels/CASE" with body '{"gtin":"24006381333935","childGtin":"4006381333931","qtyOfChild":12,"isOrderable":true,"isDespatchUnit":true,"dims":{"lengthMm":400,"widthMm":300,"heightMm":200,"weightG":6000}}'
    Then the response field "version" is "4"
    And the outbox holds 4 messages

  Scenario Outline: A new declaration of a case is judged against the weight of its contents
    Given "SKU-1" has declared dimensions 100 x 50 x 40 mm and 500 g
    When I PUT "/products/SKU-1/pack-levels/CASE" with body '{"gtin":"24006381333935","childGtin":"4006381333931","qtyOfChild":12,"dims":{"lengthMm":400,"widthMm":300,"heightMm":200,"weightG":<weight>}}'
    Then the response status is <status>

    Examples: A case of 12 eaches of 500 g weighs at least 6000 g
      | weight | status |
      | 6000   | 201    |
      | 6001   | 201    |

  Scenario: A pack is not checked against a child whose weight is not known
    When I PUT "/products/SKU-1/pack-levels/CASE" with body '{"gtin":"24006381333935","childGtin":"4006381333931","qtyOfChild":12,"dims":{"lengthMm":400,"widthMm":300,"heightMm":200,"weightG":1}}'
    Then the response status is 201

  Scenario Outline: The measured weight of the each wins over the declared one when judging a case
    Given "SKU-1" has declared dimensions 100 x 50 x 40 mm and 500 g
    And the PUT "/products/SKU-1/dimensions/measured" with body '{"lengthMm":100,"widthMm":50,"heightMm":40,"weightG":400,"measuredAt":"2026-10-06T14:05:00Z"}' has been accepted
    When I PUT "/products/SKU-1/pack-levels/CASE" with body '{"gtin":"24006381333935","childGtin":"4006381333931","qtyOfChild":12,"dims":{"lengthMm":400,"widthMm":300,"heightMm":200,"weightG":<weight>}}'
    Then the response status is <status>

    Examples: 12 eaches of 400 g measured weigh 4800 g
      | weight | status |
      | 4800   | 201    |
      | 4799   | 409    |

  Scenario Outline: Changing the declared weight of an active case is judged against its contents again
    Given "SKU-1" has declared dimensions 100 x 50 x 40 mm and 500 g
    And "SKU-1" has a case of 12 eaches weighing 6000 g with GTIN "24006381333935"
    When I PUT "/products/SKU-1/pack-levels/CASE" with body '{"gtin":"24006381333935","childGtin":"4006381333931","qtyOfChild":12,"dims":{"lengthMm":400,"widthMm":300,"heightMm":200,"weightG":<weight>}}'
    Then the response status is <status>

    Examples:
      | weight | status |
      | 6001   | 200    |
      | 5999   | 409    |

  Scenario Outline: A measured pack weighs at least its contents
    Given "SKU-1" has declared dimensions 100 x 50 x 40 mm and 500 g
    And "SKU-1" has a case of 12 eaches with GTIN "24006381333935"
    When I PUT "/products/SKU-1/pack-levels/CASE/measured" with body '{"lengthMm":410,"widthMm":310,"heightMm":210,"weightG":<weight>,"measuredAt":"2026-10-06T14:05:00Z"}'
    Then the response status is <status>
    And the outbox holds <messages> messages

    Examples:
      | weight | status | messages |
      | 6000   | 200    | 5        |
      | 5999   | 409    | 4        |

  Scenario Outline: Only an active level can be measured
    Given "SKU-1" has a case of 12 eaches with GTIN "24006381333935"
    And the POST "/products/SKU-1/pack-levels/CASE/retirements" with body '{}' has been accepted
    When I PUT "/products/SKU-1/pack-levels/<level>/measured" with body '{"lengthMm":410,"widthMm":310,"heightMm":210,"weightG":6900,"measuredAt":"2026-10-06T14:05:00Z"}'
    Then the response status is 404
    And the problem type is "pack-level-not-found"

    Examples:
      | level  |
      | CASE   |
      | PALLET |

  Scenario Outline: A pack measurement obeys the rules of a unit measurement
    Given "SKU-1" has a case of 12 eaches with GTIN "24006381333935"
    When I PUT "/products/SKU-1/pack-levels/CASE/measured" with body '<body>'
    Then the response status is <status>
    And the problem type is "<slug>"
    And the outbox holds 3 messages

    Examples:
      | body                                                                                              | status | slug                  |
      | {"lengthMm":410,"widthMm":310,"heightMm":210,"weightG":6900,"measuredAt":"2026-10-06T15:00:01Z"} | 400    | measured-at-in-future |
      | {"lengthMm":0,"widthMm":310,"heightMm":210,"weightG":6900,"measuredAt":"2026-10-06T14:05:00Z"}   | 400    | invalid-dimension     |
      | {"lengthMm":410,"widthMm":310,"heightMm":210,"weightG":6900}                                      | 400    | malformed-request     |

  Scenario: A pack measurement replaces an older one, an older reading is stale and the same reading changes nothing
    Given "SKU-1" has a case of 12 eaches with GTIN "24006381333935"
    And the PUT "/products/SKU-1/pack-levels/CASE/measured" with body '{"lengthMm":410,"widthMm":310,"heightMm":210,"weightG":6900,"measuredAt":"2026-10-06T14:05:00Z"}' has been accepted
    When I PUT "/products/SKU-1/pack-levels/CASE/measured" with body '{"lengthMm":410,"widthMm":310,"heightMm":210,"weightG":6800,"measuredAt":"2026-10-06T13:00:00Z"}'
    Then the response status is 409
    And the problem type is "stale-measurement"
    When I PUT "/products/SKU-1/pack-levels/CASE/measured" with body '{"lengthMm":410,"widthMm":310,"heightMm":210,"weightG":6900,"measuredAt":"2026-10-06T14:05:00Z"}'
    Then the response status is 200
    And the outbox holds 4 messages
    When I PUT "/products/SKU-1/pack-levels/CASE/measured" with body '{"lengthMm":410,"widthMm":310,"heightMm":210,"weightG":6950,"measuredAt":"2026-10-06T14:30:00Z"}'
    Then the response status is 200
    And the response field "measured.weightG" is "6950"
    And the response field "version" is "5"
    And the outbox holds 5 messages

  Scenario Outline: A retirement without a reason is recorded as retired
    Given "SKU-1" has a case of 12 eaches with GTIN "24006381333935"
    When I POST "/products/SKU-1/pack-levels/CASE/retirements" with body '<body>'
    Then the response status is 200
    And the last published "ProductPackLevelRetired" event data is '{"sku":"SKU-1","level":"CASE","gtin":"24006381333935","effective_to":"2026-10-06T15:00:00Z","reason":"retired","version":4}'

    Examples:
      | body          |
      | {}            |
      | {"reason":""} |

  Scenario Outline: A retirement reason is at most 200 characters
    Given "SKU-1" has a case of 12 eaches with GTIN "24006381333935"
    When I retire the "CASE" level of "SKU-1" with a reason of <count> characters
    Then the response status is <status>
    And the outbox holds <messages> messages

    Examples:
      | count | status | messages |
      | 200   | 200    | 4        |
      | 201   | 400    | 3        |

  Scenario: A control character in a retirement reason is refused and retires nothing
    Given "SKU-1" has a case of 12 eaches with GTIN "24006381333935"
    When I POST "/products/SKU-1/pack-levels/CASE/retirements" with body '{"reason":"bad\u0007"}'
    Then the response status is 400
    And the problem type is "invalid-reason"
    When I GET "/gtins/24006381333935"
    Then the response field "active" is "true"

  Scenario: A retired level can be declared again with a new GTIN without superseding anything
    Given "SKU-1" has a case of 12 eaches with GTIN "24006381333935"
    And the POST "/products/SKU-1/pack-levels/CASE/retirements" with body '{"reason":"discontinued"}' has been accepted
    When I PUT "/products/SKU-1/pack-levels/CASE" with body '{"gtin":"34006381333932","childGtin":"4006381333931","qtyOfChild":24}'
    Then the response field "version" is "5"
    And the outbox holds 5 messages
    And the outbox event types end with "ProductPackLevelRetired, ProductPackLevelDeclared"
    When I GET "/gtins/24006381333935"
    Then the response field "active" is "false"
    When I GET "/gtins/34006381333932"
    Then the response field "active" is "true"
    And the response field "unitsPerPack" is "24"

  Scenario: A case cannot be declared over an each that has been retired
    Given the POST "/products/SKU-1/pack-levels/EACH/retirements" with body '{"reason":"relabelled"}' has been accepted
    When I PUT "/products/SKU-1/pack-levels/CASE" with body '{"gtin":"24006381333935","childGtin":"4006381333931","qtyOfChild":12}'
    Then the response status is 409
    And the problem type is "pack-level-order"

  Scenario: The pack levels are listed lowest level first, oldest first within a level, retired ones included
    Given "SKU-1" has a case of 12 eaches with GTIN "24006381333935"
    And the service clock reads "2026-10-07T09:00:00Z"
    And the PUT "/products/SKU-1/pack-levels/CASE" with body '{"gtin":"14006381333938","childGtin":"4006381333931","qtyOfChild":24}' has been accepted
    When I GET "/products/SKU-1/pack-levels"
    Then the response status is 200
    And the response field "version" is "5"
    And the response list "items" has 3 items
    And the response field "items.0.level" is "EACH"
    And the response field "items.1.gtin" is "24006381333935"
    And the response field "items.1.active" is "false"
    And the response field "items.2.gtin" is "14006381333938"
    And the response field "items.2.active" is "true"

  Scenario: The pack levels page like every collection
    Given "SKU-1" has a case of 12 eaches with GTIN "24006381333935"
    When I GET "/products/SKU-1/pack-levels?limit=1"
    Then the response list "items" has 1 items
    And the response field "items.0.level" is "EACH"
    And there is a next page
    When I GET the next page of "/products/SKU-1/pack-levels?limit=1"
    Then the response field "items.0.level" is "CASE"
    And there is no next page
