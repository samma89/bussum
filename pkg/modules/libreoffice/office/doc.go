// Package office converts Office documents to Markdown.
//
// LibreOffice exports HTML through the UNO API. [htmlmarkdown.ConvertHTMLBytes]
// turns that HTML into Markdown, which is the same converter the HTML engine
// uses. The module ID is office. Operators select it with --markdown-engines
// (env MARKDOWN_ENGINES).
package office
