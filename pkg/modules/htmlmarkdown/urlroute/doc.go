// Package urlroute serves POST /forms/markdown/convert/url.
//
// Chromium renders the page, including its JavaScript, and
// [htmlmarkdown.ConvertHTMLBytes] turns the resulting HTML into Markdown.
// The LibreOffice image does not import this package, so it does not
// register the route or pull Chromium into that binary.
package urlroute
