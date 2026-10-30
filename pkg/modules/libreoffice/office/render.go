package office

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/JohannesKaufmann/html-to-markdown/v2/converter"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/base"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/commonmark"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/strikethrough"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/table"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"github.com/gotenberg/gotenberg/v8/pkg/modules/markdown"
)

// markdownFromExport turns a LibreOffice HTML export directory into Markdown.
// mainPath is the HTML file unoconverter was asked to write. Companion HTML
// files in root are slide or sheet pages.
func markdownFromExport(ctx context.Context, root, mainPath string, kind docKind, names []string, options markdown.Options) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("convert html to markdown: %w", err)
	}

	raw, err := loadDocument(root, mainPath, kind, names)
	if err != nil {
		return nil, err
	}

	doc, err := parseHTML(raw)
	if err != nil {
		return nil, fmt.Errorf("parse html export: %w", err)
	}

	// LibreOffice wraps a sheet title in <a name="table0">. A table inside
	// that anchor cannot become a GFM table, so the anchor is unwrapped.
	unwrapNamedAnchors(doc)

	if kind == kindSheet {
		ensureSheetHeadings(doc, names)
	}

	gfm := options.Flavour == markdown.FlavourGFM
	promoteInlineStyles(doc, gfm)

	if gfm {
		markTaskLists(doc)
	}

	dropEmptyParagraphs(doc)

	err = rewriteImages(doc, root, options, documentStem(options.Filename))
	if err != nil {
		return nil, err
	}

	if err = ctx.Err(); err != nil {
		return nil, fmt.Errorf("convert html to markdown: %w", err)
	}

	out, err := newConverter(options.Flavour).ConvertNode(doc, converter.WithContext(ctx))
	if err != nil {
		return nil, fmt.Errorf("convert html to markdown: %w", err)
	}

	out = bytes.TrimSpace(out)
	if gfm {
		// The commonmark escaper treats "[" as syntax. The task markers added
		// above come back as "\[x]" and "\[ ]". GFM only recognises them bare.
		out = unescapeTaskMarkers(out)
	}

	if len(out) > 0 {
		out = append(out, '\n')
	}

	return out, nil
}

// taskMarker matches a list item whose checkbox was escaped by the converter.
var taskMarker = regexp.MustCompile(`(?m)^([ \t]*[-*+] )\\\[([ xX])\]`)

func unescapeTaskMarkers(markdown []byte) []byte {
	return taskMarker.ReplaceAll(markdown, []byte("${1}[${2}]"))
}

// newConverter builds the HTML-to-Markdown converter for one flavour.
//
// html-to-markdown v2.5.2 has no GFM plugin. Tables and strikethrough are
// separate plugins, which is the GFM behaviour office documents need.
// commonmark leaves those plugins off, so the same HTML becomes the strict subset.
func newConverter(flavour markdown.Flavour) *converter.Converter {
	plugins := []converter.Plugin{
		base.NewBasePlugin(),
		commonmark.NewCommonmarkPlugin(),
	}

	if flavour == markdown.FlavourGFM {
		plugins = append(plugins,
			strikethrough.NewStrikethroughPlugin(),
			table.NewTablePlugin(
				table.WithHeaderPromotion(true),
				table.WithSkipEmptyRows(true),
			),
		)
	}

	return converter.NewConverter(converter.WithPlugins(plugins...))
}

func unwrapNamedAnchors(doc *html.Node) {
	var anchors []*html.Node
	collect(doc, "a", &anchors)

	for _, anchor := range anchors {
		href := strings.TrimSpace(attr(anchor, "href"))
		if href != "" && href != "#" {
			continue
		}

		if !hasAttr(anchor, "name") && !hasAttr(anchor, "id") {
			continue
		}

		unwrap(anchor)
	}
}

func ensureSheetHeadings(doc *html.Node, names []string) {
	var tables []*html.Node
	eachTopTable(doc, func(table *html.Node) {
		tables = append(tables, table)
	})

	for index, table := range tables {
		if headingBefore(table) {
			continue
		}

		title := takeCaption(table)
		if title == "" {
			title = sheetHeading(names, index)
		}

		insertHeading(table, title)
	}
}

func eachTopTable(n *html.Node, fn func(*html.Node)) {
	if n == nil {
		return
	}

	if n.Type == html.ElementNode && n.Data == "table" {
		fn(n)
		return
	}

	for child := n.FirstChild; child != nil; child = child.NextSibling {
		eachTopTable(child, fn)
	}
}

func headingBefore(table *html.Node) bool {
	for prev := table.PrevSibling; prev != nil; prev = prev.PrevSibling {
		if prev.Type == html.TextNode && strings.TrimSpace(prev.Data) == "" {
			continue
		}

		if prev.Type != html.ElementNode {
			return false
		}

		if isHeading(prev) {
			return true
		}

		if prev.Data == "br" {
			continue
		}

		return false
	}

	return false
}

func takeCaption(table *html.Node) string {
	for child := table.FirstChild; child != nil; child = child.NextSibling {
		if child.Type == html.ElementNode && child.Data == "caption" {
			title := plainText(child)
			table.RemoveChild(child)

			return title
		}
	}

	return ""
}

// promoteInlineStyles turns LibreOffice CSS into elements the converter knows.
// The strikethrough plugin only matches del, s, and strike. Writer emits
// span style="text-decoration: line-through" instead.
func promoteInlineStyles(doc *html.Node, gfm bool) {
	var nodes []*html.Node
	walkElements(doc, func(n *html.Node) {
		nodes = append(nodes, n)
	})

	for _, node := range nodes {
		if !isInlineWrapper(node) {
			continue
		}

		style := strings.ToLower(attr(node, "style"))
		strike := gfm && strings.Contains(style, "line-through")
		bold := styleIndicatesBold(style)
		italic := styleIndicatesItalic(style)

		switch {
		case strike:
			rename(node, atom.Del, "del")
		case bold && italic:
			rename(node, atom.Strong, "strong")
			wrapChildren(node, atom.Em, "em")
		case bold:
			rename(node, atom.Strong, "strong")
		case italic:
			rename(node, atom.Em, "em")
		}
	}
}

func isInlineWrapper(n *html.Node) bool {
	switch n.Data {
	case "span", "font", "bdo":
		return true
	default:
		return false
	}
}

func styleIndicatesBold(style string) bool {
	return strings.Contains(style, "font-weight:bold") ||
		strings.Contains(style, "font-weight: bold") ||
		strings.Contains(style, "font-weight:700") ||
		strings.Contains(style, "font-weight: 700")
}

func styleIndicatesItalic(style string) bool {
	return strings.Contains(style, "font-style:italic") ||
		strings.Contains(style, "font-style: italic")
}

func wrapChildren(n *html.Node, kind atom.Atom, data string) {
	if n.FirstChild == nil {
		return
	}

	wrapper := &html.Node{Type: html.ElementNode, Data: data, DataAtom: kind}
	for n.FirstChild != nil {
		child := n.FirstChild
		n.RemoveChild(child)
		wrapper.AppendChild(child)
	}

	n.AppendChild(wrapper)
}

func walkElements(n *html.Node, fn func(*html.Node)) {
	if n.Type == html.ElementNode {
		fn(n)
	}

	for child := n.FirstChild; child != nil; child = child.NextSibling {
		walkElements(child, fn)
	}
}

func markTaskLists(doc *html.Node) {
	var items []*html.Node
	collect(doc, "li", &items)

	for _, item := range items {
		markTaskListItem(item)
	}
}

func markTaskListItem(item *html.Node) {
	target := item
	if element := firstElement(item); element != nil && element.Data == "p" {
		target = element
	}

	if element := firstElement(target); element != nil && element.Data == "input" && strings.EqualFold(attr(element, "type"), "checkbox") {
		box := "[ ] "
		if hasAttr(element, "checked") {
			box = "[x] "
		}

		target.RemoveChild(element)

		if text := firstText(target); text != nil {
			text.Data = strings.TrimLeft(text.Data, " \t\n\r")
		}

		target.InsertBefore(&html.Node{Type: html.TextNode, Data: box}, target.FirstChild)

		return
	}

	if text := firstText(target); text != nil {
		if prefix, rest, ok := taskMark(text.Data); ok {
			text.Data = prefix + rest
		}
	}
}

func taskMark(value string) (string, string, bool) {
	rest := strings.TrimLeft(value, " \t\n\r")
	marks := []struct {
		from string
		to   string
	}{
		{from: "☐", to: "[ ] "},
		{from: "□", to: "[ ] "},
		{from: "☑", to: "[x] "},
		{from: "☒", to: "[x] "},
		{from: "✓", to: "[x] "},
		{from: "✔", to: "[x] "},
	}

	for _, mark := range marks {
		if strings.HasPrefix(rest, mark.from) {
			return mark.to, strings.TrimLeft(rest[len(mark.from):], " \t"), true
		}
	}

	return "", "", false
}

func dropEmptyParagraphs(doc *html.Node) {
	var empty []*html.Node
	walkElements(doc, func(n *html.Node) {
		if isEmptyParagraph(n) {
			empty = append(empty, n)
		}
	})

	for _, node := range empty {
		if node.Parent != nil {
			node.Parent.RemoveChild(node)
		}
	}
}

func isEmptyParagraph(n *html.Node) bool {
	if n.Type != html.ElementNode || n.Data != "p" {
		return false
	}

	for child := n.FirstChild; child != nil; child = child.NextSibling {
		switch child.Type {
		case html.TextNode:
			if strings.TrimSpace(child.Data) != "" {
				return false
			}
		case html.ElementNode:
			if child.Data != "br" {
				return false
			}
		case html.ErrorNode, html.DocumentNode, html.CommentNode, html.DoctypeNode, html.RawNode:
			return false
		}
	}

	return true
}

func pathUnescape(value string) (string, error) {
	decoded, err := url.PathUnescape(value)
	if err != nil {
		return "", err
	}

	return decoded, nil
}
