@markdown
@markdown-convert
@markdown-convert-html
Feature: HTML to Markdown

  Scenario: POST /forms/markdown/convert (HTML)
    Given I have a default Gotenberg container
    When I make a "POST" request to Gotenberg at the "/forms/markdown/convert" endpoint with the following form data and header(s):
      | files           | testdata/markdown/page.html | file   |
      | Gotenberg-Trace | markdown_html               | header |
    Then the response status code should be 200
    Then the response header "Gotenberg-Trace" should be "markdown_html"
    Then the response body should contain string:
      """
      # HTML title
      """
    Then the response body should contain string:
      """
      nested
      """
    Then the response body should contain string:
      """
      fmt.Println("hi")
      """
    Then there should be the following file(s) in the response:
      | page.md |

  Scenario: POST /forms/markdown/convert (HTM)
    Given I have a default Gotenberg container
    When I make a "POST" request to Gotenberg at the "/forms/markdown/convert" endpoint with the following form data and header(s):
      | files | testdata/markdown/page.htm | file |
    Then the response status code should be 200
    Then the response body should contain string:
      """
      # HTM title
      """
    Then there should be the following file(s) in the response:
      | page.md |

  Scenario: POST /forms/markdown/convert (HTML extract images)
    Given I have a default Gotenberg container
    When I make a "POST" request to Gotenberg at the "/forms/markdown/convert" endpoint with the following form data and header(s):
      | files         | testdata/markdown/page.html | file  |
      | extractImages | true                        | field |
    Then the response status code should be 200
    Then the response header "Content-Type" should be "application/zip"
    Then there should be the following file(s) in the response:
      | page.md    |
      | page-1.png |

  Scenario: POST /forms/markdown/convert/url (JavaScript)
    Given I have a default Gotenberg container
    Given I have a static server
    When I make a "POST" request to Gotenberg at the "/forms/markdown/convert/url" endpoint with the following form data and header(s):
      | url               | http://host.docker.internal:%d/html/testdata/markdown/js-page.html | field  |
      | waitForExpression | window.__ready === true                                            | field  |
      | Gotenberg-Trace   | markdown_url                                                       | header |
    Then the response status code should be 200
    Then the response header "Gotenberg-Trace" should be "markdown_url"
    Then the response body should contain string:
      """
      Rendered by JavaScript
      """
    Then there should be the following file(s) in the response:
      | document.md |

  Scenario: POST /forms/markdown/convert/url (file scheme rejected)
    Given I have a default Gotenberg container
    When I make a "POST" request to Gotenberg at the "/forms/markdown/convert/url" endpoint with the following form data and header(s):
      | url | file:///etc/passwd | field |
    Then the response status code should be 400
    Then the response body should contain string:
      """
      file:// URLs are not accepted
      """
