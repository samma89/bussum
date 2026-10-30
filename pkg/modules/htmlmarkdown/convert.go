package htmlmarkdown

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/JohannesKaufmann/html-to-markdown/v2/converter"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/base"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/commonmark"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/strikethrough"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/table"
	"golang.org/x/net/html"

	"github.com/gotenberg/gotenberg/v8/pkg/modules/markdown"
)

// maxHTMLBytes bounds one document. The API body limit usually stops a
// request first. This bound still applies when an engine calls
// [ConvertHTMLBytes] directly, and when Chromium returns a very large page.
const maxHTMLBytes = 32 << 20

// ConvertHTMLBytes writes Markdown for an HTML document already in memory.
//
// inputPath is the directory root for relative images. It may be empty when
// the HTML came from a URL and there is no local file. Remote image URLs
// are left as links. The function does not fetch them.
func ConvertHTMLBytes(ctx context.Context, logger *slog.Logger, inputPath string, document []byte, outputPath string, options markdown.Options) error {
	if ctx == nil {
		return errors.New("convert html to markdown: nil context")
	}

	if err := ctx.Err(); err != nil {
		return fmt.Errorf("convert html to markdown: %w", err)
	}

	if len(document) > maxHTMLBytes {
		return fmt.Errorf("convert html to markdown: document is %d bytes, the limit is %d", len(document), maxHTMLBytes)
	}

	flavour, err := resolveFlavour(options)
	if err != nil {
		return fmt.Errorf("convert html to markdown: %w", err)
	}

	if options.ExtractImages && strings.TrimSpace(options.ImageDir) == "" {
		return errors.New("convert html to markdown: extractImages is true and ImageDir is empty")
	}

	document = bytes.TrimPrefix(document, []byte{0xef, 0xbb, 0xbf})

	doc, err := html.Parse(bytes.NewReader(document))
	if err != nil {
		return fmt.Errorf("parse html: %w", err)
	}

	removeNoise(doc)

	if options.ExtractImages {
		err = extractImages(ctx, logger, doc, inputPath, options)
		if err != nil {
			return err
		}
	}

	if err = ctx.Err(); err != nil {
		return fmt.Errorf("convert html to markdown: %w", err)
	}

	var rendered bytes.Buffer
	err = html.Render(&rendered, doc)
	if err != nil {
		return fmt.Errorf("render html: %w", err)
	}

	conv := newConverter(flavour)
	out, err := conv.ConvertReader(bytes.NewReader(rendered.Bytes()))
	if err != nil {
		return fmt.Errorf("convert html to markdown: %w", err)
	}

	err = os.WriteFile(outputPath, out, 0o600) //nolint:gosec
	if err != nil {
		return fmt.Errorf("write markdown file: %w", err)
	}

	if logger != nil {
		logger.DebugContext(ctx, fmt.Sprintf("html converted %d bytes to %d bytes of markdown", len(document), len(out)))
	}

	return nil
}

func resolveFlavour(options markdown.Options) (markdown.Flavour, error) {
	if options.Flavour == "" {
		return markdown.FlavourGFM, nil
	}

	flavour, err := markdown.ParseFlavour(string(options.Flavour))
	if err != nil {
		return "", fmt.Errorf("flavour: %w", err)
	}

	return flavour, nil
}

func newConverter(flavour markdown.Flavour) *converter.Converter {
	plugins := []converter.Plugin{
		base.NewBasePlugin(),
		// The list-end comment is an HTML marker the library inserts between
		// two lists so a parser does not merge them. A blank line already
		// separates those lists, so the marker is noise in the Markdown.
		commonmark.NewCommonmarkPlugin(
			commonmark.WithListEndComment(false),
		),
		documentPlugin{},
	}

	// v2 publishes the table and strikethrough plugins separately. The
	// combined GitHub Flavored Markdown plugin is still marked planned, so
	// FlavourGFM enables those two plus autolinks. CommonMark does not.
	if flavour == markdown.FlavourGFM {
		plugins = append(plugins, gfmPlugin{})
	}

	return converter.NewConverter(converter.WithPlugins(plugins...))
}

// documentPlugin drops navigation chrome and keeps noscript text.
//
// The base plugin removes noscript. For a file that is not rendered in a
// browser, that text is the content, so it stays.
type documentPlugin struct{}

func (documentPlugin) Name() string { return "htmlmarkdown" }

func (documentPlugin) Init(conv *converter.Converter) error {
	conv.Register.TagType("nav", converter.TagTypeRemove, converter.PriorityEarly)
	conv.Register.TagType("noscript", converter.TagTypeBlock, converter.PriorityEarly)

	return nil
}

// gfmPlugin adds the GitHub Flavored Markdown syntax this module emits.
type gfmPlugin struct{}

func (gfmPlugin) Name() string { return "gfm" }

func (gfmPlugin) Init(conv *converter.Converter) error {
	conv.Register.Plugin(table.NewTablePlugin(
		table.WithHeaderPromotion(true),
	))
	conv.Register.Plugin(strikethrough.NewStrikethroughPlugin())
	conv.Register.Renderer(renderGFMAutolink, converter.PriorityEarly)

	return nil
}

func renderGFMAutolink(_ converter.Context, w converter.Writer, n *html.Node) converter.RenderStatus {
	if n.Type != html.ElementNode || !strings.EqualFold(n.Data, "a") {
		return converter.RenderTryNext
	}

	href := strings.TrimSpace(nodeAttr(n, "href"))
	if href == "" || strings.TrimSpace(nodeAttr(n, "title")) != "" {
		return converter.RenderTryNext
	}

	scheme := strings.ToLower(href)
	if !strings.HasPrefix(scheme, "https://") && !strings.HasPrefix(scheme, "http://") {
		return converter.RenderTryNext
	}

	if strings.ContainsAny(href, " <>") {
		return converter.RenderTryNext
	}

	if strings.TrimSpace(textContent(n)) != href {
		return converter.RenderTryNext
	}

	// A failed write must not be reported as a finished autolink. The
	// commonmark renderer then gets the node.
	_, err := w.WriteString("<" + href + ">")
	if err != nil {
		return converter.RenderTryNext
	}

	return converter.RenderSuccess
}

func removeNoise(n *html.Node) {
	for child := n.FirstChild; child != nil; {
		next := child.NextSibling
		if noiseNode(child) {
			n.RemoveChild(child)
		} else {
			removeNoise(child)
		}

		child = next
	}
}

func noiseNode(n *html.Node) bool {
	if n.Type != html.ElementNode {
		return false
	}

	switch strings.ToLower(n.Data) {
	case "script", "style", "nav":
		return true
	}

	return strings.EqualFold(strings.TrimSpace(nodeAttr(n, "role")), "navigation")
}

func nodeAttr(n *html.Node, key string) string {
	for _, attribute := range n.Attr {
		if strings.EqualFold(attribute.Key, key) {
			return attribute.Val
		}
	}

	return ""
}

func setNodeAttr(n *html.Node, key, value string) {
	for i := range n.Attr {
		if strings.EqualFold(n.Attr[i].Key, key) {
			n.Attr[i].Val = value
			return
		}
	}

	n.Attr = append(n.Attr, html.Attribute{Key: key, Val: value})
}

func textContent(n *html.Node) string {
	var builder strings.Builder

	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.TextNode {
			builder.WriteString(node.Data)
		}

		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(n)

	return builder.String()
}
