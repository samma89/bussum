// Package office converts Office documents to Markdown.
//
// LibreOffice exports HTML through the UNO API. The HTML is then converted
// with github.com/JohannesKaufmann/html-to-markdown/v2. The module ID is
// office. Operators select it with --markdown-engines (env MARKDOWN_ENGINES).
package office
