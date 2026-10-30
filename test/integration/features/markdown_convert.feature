@markdown
@markdown-convert
Feature: /forms/markdown/convert

  Scenario: POST /forms/markdown/convert (Text)
    Given I have a default Gotenberg container
    When I make a "POST" request to Gotenberg at the "/forms/markdown/convert" endpoint with the following form data and header(s):
      | files           | testdata/markdown/hello.txt | file   |
      | Gotenberg-Trace | markdown_txt                | header |
    Then the response status code should be 200
    Then the response header "Gotenberg-Trace" should be "markdown_txt"
    Then the response body should contain string:
      """
      Hello from plaintext.
      """
    Then there should be the following file(s) in the response:
      | hello.md |

  Scenario: POST /forms/markdown/convert (Markdown passthrough)
    Given I have a default Gotenberg container
    When I make a "POST" request to Gotenberg at the "/forms/markdown/convert" endpoint with the following form data and header(s):
      | files | testdata/markdown/hello.md | file |
    Then the response status code should be 200
    Then the response body should contain string:
      """
      Already markdown
      """
    Then there should be the following file(s) in the response:
      | hello.md |

  Scenario: POST /forms/markdown/convert (Many files)
    Given I have a default Gotenberg container
    When I make a "POST" request to Gotenberg at the "/forms/markdown/convert" endpoint with the following form data and header(s):
      | files | testdata/markdown/alpha.txt | file |
      | files | testdata/markdown/beta.txt  | file |
    Then the response status code should be 200
    Then the response header "Content-Type" should be "application/zip"
    Then there should be the following file(s) in the response:
      | alpha.md |
      | beta.md  |

  Scenario: POST /forms/markdown/convert (Output filename)
    Given I have a default Gotenberg container
    When I make a "POST" request to Gotenberg at the "/forms/markdown/convert" endpoint with the following form data and header(s):
      | files                     | testdata/markdown/hello.txt | file   |
      | Gotenberg-Output-Filename | my-notes                    | header |
    Then the response status code should be 200
    Then there should be the following file(s) in the response:
      | my-notes.md |

  Scenario: POST /forms/markdown/convert (Extract images, plaintext has none)
    Given I have a default Gotenberg container
    When I make a "POST" request to Gotenberg at the "/forms/markdown/convert" endpoint with the following form data and header(s):
      | files         | testdata/markdown/hello.txt | file  |
      | extractImages | true                        | field |
    Then the response status code should be 200
    Then the response body should contain string:
      """
      Hello from plaintext.
      """
    Then there should be the following file(s) in the response:
      | hello.md |

  Scenario: POST /forms/markdown/convert (Default flavour from the environment)
    Given I have a Gotenberg container with the following environment variable(s):
      | MARKDOWN_FLAVOUR | commonmark |
    When I make a "POST" request to Gotenberg at the "/forms/markdown/convert" endpoint with the following form data and header(s):
      | files | testdata/markdown/hello.txt | file |
    Then the response status code should be 200
    Then the response body should contain string:
      """
      Hello from plaintext.
      """

  Scenario: POST /forms/markdown/convert (Unsupported extension)
    Given I have a default Gotenberg container
    When I make a "POST" request to Gotenberg at the "/forms/markdown/convert" endpoint with the following form data and header(s):
      | files | testdata/markdown/notes.xyz | file |
    Then the response status code should be 400
    Then the response body should contain string:
      """
      unsupported extension '.xyz'
      """

  Scenario: POST /forms/markdown/convert (Invalid flavour)
    Given I have a default Gotenberg container
    When I make a "POST" request to Gotenberg at the "/forms/markdown/convert" endpoint with the following form data and header(s):
      | files   | testdata/markdown/hello.txt | file  |
      | flavour | latex                       | field |
    Then the response status code should be 400
    Then the response body should contain string:
      """
      form field 'flavour' is invalid
      """

  Scenario: POST /forms/markdown/convert (Routes disabled)
    Given I have a Gotenberg container with the following environment variable(s):
      | MARKDOWN_DISABLE_ROUTES | true |
    When I make a "POST" request to Gotenberg at the "/forms/markdown/convert" endpoint with the following form data and header(s):
      | files | testdata/markdown/hello.txt | file |
    Then the response status code should be 404
