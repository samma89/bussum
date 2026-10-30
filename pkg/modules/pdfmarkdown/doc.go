// Package pdfmarkdown converts PDF files to Markdown.
//
// The [Pdf] module claims the .pdf extension. It shells out to Poppler
// pdftohtml and rebuilds headings, lists, tables, and reading order from
// the XML. Register it with the other standard modules. The markdown
// package must not import this one.
package pdfmarkdown
