# Derived from: apis/openapi.yaml (listProducts: limit, cursor, handlingTag, classified; the
# nextCursor example U0tVLTE is the base64url of "SKU-1")
@bdd
Feature: Listing products: order, page boundaries and combined filters
  Products come back in ascending SKU order whatever the order they were
  registered in; a page that ends exactly at the last product has no
  nextCursor; filters combine with AND and with paging.

  Scenario: Products are listed in ascending SKU order, not registration order
    Given the product "C-3" is registered with description "Third"
    And the product "A-1" is registered with description "First"
    And the product "B-2" is registered with description "Second"
    When I GET "/products"
    Then the response status is 200
    And the listed SKUs are "A-1, B-2, C-3"
    And there is no next page

  Scenario: A page that ends exactly at the last product has no next page
    Given the product "A-1" is registered with description "First"
    And the product "B-2" is registered with description "Second"
    When I list products with query "limit=2"
    Then the listed SKUs are "A-1, B-2"
    And there is no next page
    When I list products with query "limit=1"
    Then the listed SKUs are "A-1"
    And there is a next page

  Scenario Outline: Filters combine and a filtered list still pages
    Given the product "P-1" is registered with description "Gas can"
    And the product "P-2" is registered with description "Glass bottle"
    And the product "P-3" is registered with description "Unclassified"
    And "P-1" is classified with tags "Hazmat"
    And "P-2" is classified with tags "Hazmat, Fragile"
    When I list products with query "<query>"
    Then the response status is 200
    And the listed SKUs are "<skus>"

    Examples:
      | query                               | skus       |
      | handlingTag=Hazmat&classified=true  | P-1, P-2   |
      | handlingTag=Hazmat&classified=false |            |
      | handlingTag=Fragile&classified=true | P-2        |
      | handlingTag=Hazmat&limit=1          | P-1        |
