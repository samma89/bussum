@markdown
@markdown-convert
@markdown-pdf
Feature: /forms/markdown/convert PDF

  Scenario: POST /forms/markdown/convert (PDF)
    Given I have a default Gotenberg container
    When I make a "POST" request to Gotenberg at the "/forms/markdown/convert" endpoint with the following form data and header(s):
      | files           | testdata/markdown/article.pdf | file   |
      | Gotenberg-Trace | markdown_pdf                  | header |
    Then the response status code should be 200
    Then the response header "Gotenberg-Trace" should be "markdown_pdf"
    Then the response body should contain string:
      """
      # Quarterly Report
      """
    Then the response body should contain string:
      """
      be read before the right side.
      """
    Then the response body should contain string:
      """
      Beta text stays on the right
      """
    Then the response body should contain string:
      """
      | Ada | 10 | Paris |
      """
    Then there should be the following file(s) in the response:
      | article.md |

  Scenario: POST /forms/markdown/convert (PDF page separator)
    Given I have a Gotenberg container with the following environment variable(s):
      | PDF_MARKDOWN_PAGE_SEPARATOR | *** |
    When I make a "POST" request to Gotenberg at the "/forms/markdown/convert" endpoint with the following form data and header(s):
      | files | testdata/markdown/article.pdf | file |
    Then the response status code should be 200
    Then the response body should contain string:
      """
      ***
      """
    Then the response body should contain string:
      """
      ## Left column
      """

  Scenario: POST /forms/markdown/convert (Encrypted PDF)
    Given I have a default Gotenberg container
    When I make a "POST" request to Gotenberg at the "/forms/markdown/convert" endpoint with the following form data and header(s):
      | files | testdata/markdown/encrypted.pdf | file |
    Then the response status code should be 400
    Then the response body should contain string:
      """
      'encrypted.pdf' is encrypted
      """

  Scenario: POST /forms/markdown/convert (Scanned PDF)
    Given I have a default Gotenberg container
    When I make a "POST" request to Gotenberg at the "/forms/markdown/convert" endpoint with the following form data and header(s):
      | files | testdata/markdown/scanned.pdf | file |
    Then the response status code should be 400
    Then the response body should contain string:
      """
      'scanned.pdf' has no extractable text
      """
