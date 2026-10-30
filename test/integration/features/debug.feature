@debug
Feature: /debug

  Scenario: GET /debug (Disabled)
    Given I have a default Gotenberg container
    When I make a "GET" request to Gotenberg at the "/debug" endpoint
    Then the response status code should be 404

  Scenario: GET /debug (Enabled)
    Given I have a Gotenberg container with the following environment variable(s):
      | API_ENABLE_DEBUG_ROUTE | true |
    When I make a "GET" request to Gotenberg at the "/debug" endpoint
    Then the response status code should be 200
    Then the response header "Content-Type" should be "application/json"
    Then the response body should contain string:
      """
      "markdown"
      """
