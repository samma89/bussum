package office

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
	"golang.org/x/net/html/charset"
)

// Impress publishes a frameset plus one HTML file per slide. textN.html is the
// slide text. imgN.html is a second copy of the same slide and is ignored when
// the text files exist. Outline and navigation pages are not slides.
var (
	textSlidePattern = regexp.MustCompile(`(?i)^text\d+$`)
	pageSlidePattern = regexp.MustCompile(`(?i)^(slide|page)\d+$`)
	imgSlidePattern  = regexp.MustCompile(`(?i)^img\d+$`)
)

func loadDocument(root, mainPath string, kind docKind, names []string) (string, error) {
	main, err := readHTMLFile(mainPath)
	if err != nil {
		return "", err
	}

	files, err := listHTML(root)
	if err != nil {
		return "", err
	}

	others := excludePath(files, mainPath)

	if kind == kindSlides {
		slides := pickSlideFiles(others)
		if isFramesetHTML(main) || len(slides) > 0 {
			if len(slides) == 0 {
				return "", fmt.Errorf("impress html export has a frameset and no slide pages")
			}

			return assembleSlides(root, slides)
		}
	}

	if kind == kindSheet && len(others) > 0 {
		return assembleSheets(root, mainPath, others, names)
	}

	return main, nil
}

func readHTMLFile(path string) (string, error) {
	// The export directory is created for this request. The path stays inside it.
	raw, err := os.ReadFile(path) //nolint:gosec // path is inside the export directory or a test fixture
	if err != nil {
		return "", fmt.Errorf("read html export: %w", err)
	}

	decoded, err := toUTF8(raw)
	if err != nil {
		return "", err
	}

	return string(decoded), nil
}

func toUTF8(raw []byte) ([]byte, error) {
	// LibreOffice writes UTF-8. Treating valid UTF-8 as windows-1252, which is
	// what the HTML sniff does when it misses the meta charset, corrupts it.
	if utf8.Valid(raw) {
		return raw, nil
	}

	encoding, name, _ := charset.DetermineEncoding(raw, "text/html")
	if name == "utf-8" || name == "utf8" || name == "unicode-1-1-utf-8" {
		return raw, nil
	}

	decoded, err := encoding.NewDecoder().Bytes(raw)
	if err != nil {
		return nil, fmt.Errorf("decode html charset %s: %w", name, err)
	}

	return decoded, nil
}

func listHTML(root string) ([]string, error) {
	var files []string

	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		if entry.IsDir() {
			return nil
		}

		switch strings.ToLower(filepath.Ext(entry.Name())) {
		case ".html", ".htm":
			files = append(files, path)
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("list html export: %w", err)
	}

	sort.Strings(files)

	return files, nil
}

func excludePath(paths []string, skip string) []string {
	skip = filepath.Clean(skip)
	out := make([]string, 0, len(paths))

	for _, path := range paths {
		if filepath.Clean(path) == skip {
			continue
		}

		out = append(out, path)
	}

	return out
}

func isFramesetHTML(raw string) bool {
	return strings.Contains(strings.ToLower(raw), "<frameset")
}

func pickSlideFiles(paths []string) []string {
	var textFiles, pageFiles, imgFiles, other []string

	for _, path := range paths {
		base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		if skipSlideName(base) {
			continue
		}

		switch {
		case textSlidePattern.MatchString(base):
			textFiles = append(textFiles, path)
		case pageSlidePattern.MatchString(base):
			pageFiles = append(pageFiles, path)
		case imgSlidePattern.MatchString(base):
			imgFiles = append(imgFiles, path)
		default:
			other = append(other, path)
		}
	}

	switch {
	case len(textFiles) > 0:
		sortByTrailingNumber(textFiles)
		return textFiles
	case len(pageFiles) > 0:
		sortByTrailingNumber(pageFiles)
		return pageFiles
	case len(imgFiles) > 0:
		sortByTrailingNumber(imgFiles)
		return imgFiles
	default:
		sortByTrailingNumber(other)
		return other
	}
}

func skipSlideName(base string) bool {
	low := strings.ToLower(base)
	switch low {
	case "outline", "index", "nav", "navbar", "frameset", "notes", "note", "document":
		return true
	}

	return strings.Contains(low, "outline") || strings.Contains(low, "navbar")
}

func sortByTrailingNumber(paths []string) {
	sort.Slice(paths, func(i, j int) bool {
		left, right := trailingNumber(paths[i]), trailingNumber(paths[j])
		if left != right {
			return left < right
		}

		return paths[i] < paths[j]
	})
}

func trailingNumber(path string) int {
	base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	index := len(base)

	for index > 0 && base[index-1] >= '0' && base[index-1] <= '9' {
		index--
	}

	if index == len(base) {
		return 0
	}

	number, err := strconv.Atoi(base[index:])
	if err != nil {
		return 0
	}

	return number
}

type fragment struct {
	html       string
	hasHeading bool
	// title is the document title. Impress puts the slide name there when the
	// body has no heading of its own.
	title string
}

func bodyFragment(path, root string) (fragment, error) {
	raw, err := readHTMLFile(path)
	if err != nil {
		return fragment{}, err
	}

	if isFramesetHTML(raw) {
		return fragment{}, nil
	}

	doc, err := parseHTML(raw)
	if err != nil {
		return fragment{}, fmt.Errorf("parse html export: %w", err)
	}

	unwrapNamedAnchors(doc)
	rebaseImages(doc, filepath.Dir(path), root)

	body := findFirst(doc, "body")
	inner, err := innerHTML(body)
	if err != nil {
		return fragment{}, fmt.Errorf("render html export: %w", err)
	}

	return fragment{
		html:       inner,
		hasHeading: leadingHeading(body),
		title:      plainText(findFirst(doc, "title")),
	}, nil
}

func leadingHeading(body *html.Node) bool {
	element := firstElement(body)
	if isHeading(element) {
		return true
	}

	// A heading wrapped only by a named anchor still titles the sheet.
	if element != nil && element.Data == "a" && isHeading(firstElement(element)) {
		return true
	}

	return false
}

func assembleSlides(root string, slides []string) (string, error) {
	var builder strings.Builder
	builder.WriteString("<!DOCTYPE html><html><head><meta charset=\"utf-8\"></head><body>\n")

	for index, path := range slides {
		part, err := bodyFragment(path, root)
		if err != nil {
			return "", err
		}

		title, bodyHTML := slideSection(path, part, index)
		fmt.Fprintf(&builder, "<section><h2>%s</h2>\n%s\n</section>\n", htmlEscape(title), bodyHTML)
	}

	builder.WriteString("</body></html>\n")

	return builder.String(), nil
}

func slideSection(path string, part fragment, index int) (string, string) {
	doc, err := parseHTML("<!DOCTYPE html><html><body>" + part.html + "</body></html>")
	if err != nil {
		return fallbackSlideTitle(index), part.html
	}

	body := findFirst(doc, "body")
	title := slideTitle(body, path, index, part.title)
	dropDuplicateTitle(body, title)

	inner, err := innerHTML(body)
	if err != nil {
		return title, part.html
	}

	return title, inner
}

func slideTitle(body *html.Node, path string, index int, docTitle string) string {
	if heading := firstHeading(body); heading != nil {
		if title := meaningfulTitle(plainText(heading), path); title != "" {
			return title
		}
	}

	if title := meaningfulTitle(docTitle, path); title != "" {
		return title
	}

	return fallbackSlideTitle(index)
}

func firstHeading(n *html.Node) *html.Node {
	if n == nil {
		return nil
	}

	if isHeading(n) {
		return n
	}

	for child := n.FirstChild; child != nil; child = child.NextSibling {
		if found := firstHeading(child); found != nil {
			return found
		}
	}

	return nil
}

func dropDuplicateTitle(body *html.Node, title string) {
	heading := firstHeading(body)
	if heading == nil || plainText(heading) != title {
		return
	}

	if heading.Parent != nil {
		heading.Parent.RemoveChild(heading)
	}
}

func meaningfulTitle(title, path string) string {
	title = strings.Join(strings.Fields(title), " ")
	if title == "" {
		return ""
	}

	low := strings.ToLower(title)
	base := strings.ToLower(strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)))
	switch low {
	case base, "untitled", "html document", "slide", "page":
		return ""
	}

	if textSlidePattern.MatchString(low) || pageSlidePattern.MatchString(low) || imgSlidePattern.MatchString(low) {
		return ""
	}

	return title
}

func fallbackSlideTitle(index int) string {
	if index < 0 {
		index = 0
	}

	return fmt.Sprintf("Slide %d", index+1)
}

func assembleSheets(root, main string, others, names []string) (string, error) {
	sortByTrailingNumber(others)

	files := make([]string, 0, 1+len(others))
	files = append(files, main)
	files = append(files, others...)

	var builder strings.Builder
	builder.WriteString("<!DOCTYPE html><html><head><meta charset=\"utf-8\"></head><body>\n")

	for index, path := range files {
		part, err := bodyFragment(path, root)
		if err != nil {
			return "", err
		}

		if strings.TrimSpace(part.html) == "" {
			continue
		}

		if !part.hasHeading {
			fmt.Fprintf(&builder, "<h2>%s</h2>\n", htmlEscape(sheetHeading(names, index)))
		}

		builder.WriteString(part.html)
		builder.WriteString("\n")
	}

	builder.WriteString("</body></html>\n")

	return builder.String(), nil
}

func sheetHeading(names []string, index int) string {
	if index >= 0 && index < len(names) && strings.TrimSpace(names[index]) != "" {
		return strings.TrimSpace(names[index])
	}

	return fmt.Sprintf("Sheet %d", index+1)
}

func htmlEscape(value string) string {
	return html.EscapeString(value)
}

// rebaseImages rewrites local image sources so they stay valid once several
// HTML files are concatenated into one document at the export root.
func rebaseImages(doc *html.Node, fromDir, root string) {
	var images []*html.Node
	collect(doc, "img", &images)

	for _, image := range images {
		source := strings.TrimSpace(attr(image, "src"))
		if source == "" || isRemoteImage(source) || isDataImage(source) {
			continue
		}

		relative, ok := relativeToRoot(fromDir, root, source)
		if !ok {
			continue
		}

		setAttr(image, "src", relative)
	}
}

func relativeToRoot(fromDir, root, source string) (string, bool) {
	decoded, err := pathUnescape(source)
	if err != nil {
		return "", false
	}

	cleaned := filepath.Clean(decoded)

	var absolute string
	if filepath.IsAbs(cleaned) {
		absolute = cleaned
	} else {
		absolute = filepath.Join(fromDir, cleaned)
	}

	absolute = filepath.Clean(absolute)
	root = filepath.Clean(root)

	relative, err := filepath.Rel(root, absolute)
	if err != nil || escapes(relative) {
		return "", false
	}

	return filepath.ToSlash(relative), true
}

func escapes(relative string) bool {
	return relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

// insertHeading puts an h2 before node.
func insertHeading(node *html.Node, title string) {
	if node == nil || node.Parent == nil {
		return
	}

	heading := &html.Node{Type: html.ElementNode, Data: "h2", DataAtom: atom.H2}
	heading.AppendChild(&html.Node{Type: html.TextNode, Data: title})
	node.Parent.InsertBefore(heading, node)
}
