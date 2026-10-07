Feature: List products
  Products are listed in ascending SKU order a page at a time; the opaque
  nextCursor of a page fetches the next one, and its absence marks the last
  page. Filters narrow the list by handling tag or by being classified.

  Background:
    Given the product "SKU-1" is registered with description "Battery"
    And the product "SKU-2" is registered with description "Glass vase"
    And the product "SKU-3" is registered with description "Frozen peas"
    And the product "SKU-4" is registered with description "Unclassified"
    And "SKU-1" is classified with tags "Hazmat"
    And "SKU-2" is classified with tags "Fragile, HighValue"
    And "SKU-3" is classified with tags "TemperatureSensitive" and temperature class "Frozen"

  Scenario: Paging through every product
    When I list products with query "limit=3"
    Then the listed SKUs are "SKU-1, SKU-2, SKU-3"
    And there is a next page
    When I list the next page
    Then the listed SKUs are "SKU-4"
    And there is no next page

  Scenario: Only products carrying a handling tag
    When I list products with query "handlingTag=Fragile"
    Then the listed SKUs are "SKU-2"

  Scenario: Only classified products
    When I list products with query "classified=true"
    Then the listed SKUs are "SKU-1, SKU-2, SKU-3"

  Scenario: Only unclassified products
    When I list products with query "classified=false"
    Then the listed SKUs are "SKU-4"

  Scenario Outline: Malformed list queries are rejected
    When I GET "/products?<query>"
    Then the response status is 400
    And the problem type is "malformed-request"

    Examples:
      | query             |
      | limit=0           |
      | limit=501         |
      | cursor=%21%21     |
      | handlingTag=Sharp |
      | classified=maybe  |
