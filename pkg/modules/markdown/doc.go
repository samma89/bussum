// Package markdown converts documents to Markdown.
//
// The [Markdown] module collects every module that implements
// [MarkdownEngine], dispatches by file extension, and serves
// POST /forms/markdown/convert. The built-in [Plaintext] engine copies
// .txt, .text, .md, and .markdown files. Office, HTML, and PDF engines
// register themselves the same way and are selected with
// --markdown-engines (env MARKDOWN_ENGINES).
package markdown
