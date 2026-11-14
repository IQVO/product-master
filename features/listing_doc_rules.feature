# Derived from: warehouse-docs product-master (use-cases: ListProducts, troubleshooting: REST errors
# for invalid-limit / invalid-cursor / unknown-query-parameter / malformed-request), apis/openapi.yaml
# (listProducts), REST API guideline paging
@bdd
Feature: Listing rules from the documentation
  Products page by SKU in byte order, 100 at a time unless told otherwise,
  between 1 and 500; filters read the current classification. The service
  clock reads 2026-10-06T15:00:00Z.

  Scenario: Without a limit a page holds 100 products
    Given the products named "P-" numbered 1 to 101 are registered
    When I GET "/products"
    Then the response status is 200
    And the response list "items" has 100 items
    And there is a next page
    When I GET the next page of "/products"
    Then the response list "items" has 1 items
    And the listed SKUs are "P-101"
    And there is no next page

  Scenario Outline: A limit from 1 to 500 is accepted
    Given the product "SKU-1" is registered with description "Battery"
    And the product "SKU-2" is registered with description "Charger"
    When I GET "/products?limit=<limit>"
    Then the response status is 200
    And the response list "items" has <items> items

    Examples:
      | limit | items |
      | 1     | 1     |
      | 500   | 2     |

  Scenario Outline: A limit that is not a single integer from 1 to 500 is invalid
    When I GET "/products?<query>"
    Then the response status is 400
    And the problem type is "invalid-limit"

    Examples:
      | query           |
      | limit=          |
      | limit=1&limit=2 |
      | limit=abc       |
      | limit=-1        |
      | limit=1.5       |

  Scenario Outline: A parameter the list does not declare, or a repeated cursor, is refused
    When I GET "/products?<query>"
    Then the response status is 400
    And the problem type is "<slug>"

    Examples:
      | query                 | slug                    |
      | offset=10             | unknown-query-parameter |
      | nextToken=abc         | unknown-query-parameter |
      | handlingtag=Fragile   | unknown-query-parameter |
      | cursor=AAAA&cursor=BB | invalid-cursor          |

  Scenario: Products are sorted by SKU in byte order, upper case before lower case
    Given the product "sku-1" is registered with description "Lower case"
    And the product "SKU-2" is registered with description "Second"
    And the product "Z-9" is registered with description "Last upper case"
    And the product "SKU-10" is registered with description "Tenth"
    When I GET "/products"
    Then the listed SKUs are "SKU-10, SKU-2, Z-9, sku-1"

  Scenario: The filters read the current classification, so reclassifying moves a product between them
    Given the product "P-1" is registered with description "Gas can"
    And the product "P-2" is registered with description "Glass vase"
    And the product "P-3" is registered with description "Unclassified"
    And "P-1" is classified with tags "Hazmat"
    And "P-2" is classified with tags "Fragile"
    When I PUT "/products/P-1/classification" with body '{"handlingTags":["Fragile"]}'
    And I list products with query "handlingTag=Hazmat"
    Then the listed SKUs are ""
    When I list products with query "handlingTag=Fragile"
    Then the listed SKUs are "P-1, P-2"
    When I list products with query "classified=false"
    Then the listed SKUs are "P-3"
    When I PUT "/products/P-3/classification" with body '{"handlingTags":["HighValue"]}'
    And I list products with query "classified=false"
    Then the listed SKUs are ""
