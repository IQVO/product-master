# Derived from: warehouse-docs product-master (troubleshooting REST errors: idempotency-key-required,
# idempotency-key-invalid, idempotency-key-reused, malformed-request; "every PUT is idempotent by itself,
# the one POST is keyed"), apis/openapi.yaml (IdempotencyKey, IdempotencyReplayed), ADR 0013
@bdd
Feature: The keyed retirement POST
  Retiring a pack level is the one POST of the service and needs a client
  generated Idempotency-Key: the same key with the same request replays the
  stored answer, with another request it is refused. The service clock reads
  2026-10-06T15:00:00Z.

  Background:
    Given the product "SKU-1" is registered with description "Battery"
    And "SKU-1" has the each GTIN "4006381333931"
    And "SKU-1" has a case of 12 eaches with GTIN "24006381333935"

  Scenario: A retirement without an Idempotency-Key is refused and retires nothing
    When I POST "/products/SKU-1/pack-levels/CASE/retirements" with body '{"reason":"discontinued"}' and no Idempotency-Key
    Then the response status is 400
    And the problem type is "idempotency-key-required"
    And the outbox holds 3 messages
    When I GET "/gtins/24006381333935"
    Then the response field "active" is "true"

  Scenario: A key is one header of 1 to 128 printable ASCII characters
    When I POST "/products/SKU-1/pack-levels/CASE/retirements" with an Idempotency-Key of 129 characters
    Then the response status is 400
    And the problem type is "idempotency-key-invalid"
    When I POST "/products/SKU-1/pack-levels/CASE/retirements" with the Idempotency-Key "café-key"
    Then the response status is 400
    And the problem type is "idempotency-key-invalid"
    When I POST "/products/SKU-1/pack-levels/CASE/retirements" with the Idempotency-Keys "first-key" and "second-key"
    Then the response status is 400
    And the problem type is "idempotency-key-invalid"
    And the outbox holds 3 messages
    When I POST "/products/SKU-1/pack-levels/CASE/retirements" with an Idempotency-Key of 128 characters
    Then the response status is 200
    And the response field "active" is "false"
    And the outbox holds 4 messages

  Scenario: A key may use every printable ASCII character from the space to the tilde
    When I POST "/products/SKU-1/pack-levels/CASE/retirements" with the Idempotency-Key "retire case 1"
    Then the response status is 200
    And the response field "active" is "false"
    When I POST "/products/SKU-1/pack-levels/EACH/retirements" with the Idempotency-Key "~"
    Then the response status is 200
    And the response field "active" is "false"

  Scenario: The same key with the same request replays the stored answer and retires once
    When I POST "/products/SKU-1/pack-levels/CASE/retirements" with body '{"reason":"discontinued"}' and the Idempotency-Key "retire-case-1"
    Then the response status is 200
    And the response field "active" is "false"
    And the response has no header "Idempotency-Replayed"
    When I POST "/products/SKU-1/pack-levels/CASE/retirements" with body '{"reason":"discontinued"}' and the Idempotency-Key "retire-case-1"
    Then the response status is 200
    And the response field "active" is "false"
    And the response header "Idempotency-Replayed" is "true"
    And the outbox holds 4 messages

  Scenario: The same key with another body or another path is a client bug and is refused
    When I POST "/products/SKU-1/pack-levels/CASE/retirements" with body '{"reason":"discontinued"}' and the Idempotency-Key "retire-case-2"
    Then the response status is 200
    When I POST "/products/SKU-1/pack-levels/CASE/retirements" with body '{"reason":"relabelled"}' and the Idempotency-Key "retire-case-2"
    Then the response status is 409
    And the problem type is "idempotency-key-reused"
    When I POST "/products/SKU-1/pack-levels/EACH/retirements" with body '{"reason":"discontinued"}' and the Idempotency-Key "retire-case-2"
    Then the response status is 409
    And the problem type is "idempotency-key-reused"
    And the outbox holds 4 messages

  Scenario: A refused request is stored too, so the same key replays the 404 even after the level exists
    When I POST "/products/SKU-1/pack-levels/PALLET/retirements" with body '{}' and the Idempotency-Key "retire-pallet-1"
    Then the response status is 404
    And the problem type is "pack-level-not-found"
    Given the PUT "/products/SKU-1/pack-levels/PALLET" with body '{"gtin":"54006381333936","childGtin":"24006381333935","qtyOfChild":50}' has been accepted
    When I POST "/products/SKU-1/pack-levels/PALLET/retirements" with body '{}' and the Idempotency-Key "retire-pallet-1"
    Then the response status is 404
    And the problem type is "pack-level-not-found"
    And the response header "Idempotency-Replayed" is "true"
    When I POST "/products/SKU-1/pack-levels/PALLET/retirements" with body '{}' and the Idempotency-Key "retire-pallet-2"
    Then the response status is 200
    And the response field "active" is "false"

  Scenario Outline: A body is at most 64 KiB on the keyed POST too
    When I POST "/products/SKU-1/pack-levels/CASE/retirements" with a JSON body padded with leading spaces to <bytes> bytes: '{"reason":"discontinued"}'
    Then the response status is <status>
    And the outbox holds <messages> messages

    Examples:
      | bytes | status | messages |
      | 65536 | 200    | 4        |
      | 65537 | 400    | 3        |
