// Package htmlmarkdown converts HTML documents to Markdown.
//
// The [HTML] module claims .html and .htm and registers itself as the
// markdown engine with ID html. [ConvertHTMLBytes] is the shared
// implementation an office engine can call after it has exported HTML.
//
// Rendering a URL is a separate route. It lives in the urlroute subpackage
// so this package does not import Chromium, and the LibreOffice image can
// convert an uploaded HTML file without starting a browser.
package htmlmarkdown
