package pdfmarkdown

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"math"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/gotenberg/gotenberg/v8/pkg/modules/markdown"
)

// rendered is one PDF turned into Markdown.
type rendered struct {
	markdown string
	hasText  bool
	images   []imageJob
}

// imageJob copies one Poppler image into the route's image directory.
type imageJob struct {
	src  string
	dest string
}

type span struct {
	text   string
	bold   bool
	italic bool
	href   string
}

type run struct {
	top, left, width, height, size float64
	plain                          string
	rich                           string
	allBold                        bool
	allItalic                      bool
	image                          bool
	src                            string
}

func (r run) bottom() float64 { return r.top + r.height }
func (r run) right() float64  { return r.left + r.width }

type segment struct {
	left, width, size float64
	plain             string
	rich              string
	allBold           bool
	allItalic         bool
	image             bool
	src               string
}

type line struct {
	top, left, width, height, size float64
	plain                          string
	rich                           string
	allBold                        bool
	allItalic                      bool
	image                          bool
	src                            string
	items                          []run
	segments                       []segment
}

func (l line) bottom() float64 { return l.top + l.height }
func (l line) right() float64  { return l.left + l.width }

type fontSpec struct {
	size   float64
	family string
}

type page struct {
	width float64
	lines []line
}

// renderXML turns pdftohtml -xml into Markdown.
//
// pdftohtml emits text in content-stream order. A two-column page is often
// stored row by row, so the layout pass splits on the vertical gutter and
// reads each column from top to bottom. Headings come from font size, not
// from tags Poppler does not write.
func renderXML(data []byte, xmlDir string, options markdown.Options, pageSeparator string) (rendered, error) {
	pages, err := parsePages(data)
	if err != nil {
		return rendered{}, err
	}

	stem := documentStem(options.Filename)
	var (
		b        strings.Builder
		images   []imageJob
		hasText  bool
		pageNum  int
		imageNum int
	)

	for _, pg := range pages {
		body := bodySize(pg.lines)
		parts, jobs, letters := renderBlocks(blocksFromLines(pg.lines, pg.width), body, options, xmlDir, stem, &imageNum)
		if letters > 0 {
			hasText = true
		}

		parts = strings.TrimSpace(parts)
		if parts == "" {
			continue
		}

		if pageNum > 0 {
			b.WriteString("\n\n")
			if pageSeparator != "" {
				b.WriteString(pageSeparator)
				b.WriteString("\n\n")
			}
		}

		pageNum++
		b.WriteString(parts)
		images = append(images, jobs...)
	}

	if b.Len() > 0 {
		b.WriteByte('\n')
	}

	return rendered{markdown: b.String(), hasText: hasText, images: images}, nil
}

func parsePages(data []byte) ([]page, error) {
	dec := xml.NewDecoder(bytes.NewReader(stripDoctype(data)))
	dec.Entity = map[string]string{"nbsp": "\u00a0"}

	var (
		pages  []page
		fonts  = map[string]fontSpec{}
		inPage bool
		cur    page
		runs   []run
	)

	for {
		tok, err := dec.Token()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}

			return nil, fmt.Errorf("parse pdftohtml xml: %w", err)
		}

		start, ok := tok.(xml.StartElement)
		if !ok {
			end, isEnd := tok.(xml.EndElement)
			if isEnd && end.Name.Local == "page" && inPage {
				cur.lines = groupLines(runs)
				pages = append(pages, cur)
				inPage = false
				runs = nil
			}

			continue
		}

		switch start.Name.Local {
		case "page":
			if inPage {
				cur.lines = groupLines(runs)
				pages = append(pages, cur)
			}

			cur = page{width: parseFloat(attr(start, "width"), 612)}
			inPage = true
			runs = nil
		case "fontspec":
			fonts[attr(start, "id")] = fontSpec{
				size:   parseFloat(attr(start, "size"), 12),
				family: attr(start, "family"),
			}
			if err := dec.Skip(); err != nil {
				return nil, fmt.Errorf("skip fontspec: %w", err)
			}
		case "text":
			if !inPage {
				if err := dec.Skip(); err != nil {
					return nil, fmt.Errorf("skip text: %w", err)
				}

				break
			}

			item, err := readText(dec, start, fonts)
			if err != nil {
				return nil, err
			}

			if strings.TrimSpace(item.plain) != "" {
				runs = append(runs, item)
			}
		case "image":
			if inPage {
				runs = append(runs, run{
					top:    parseFloat(attr(start, "top"), 0),
					left:   parseFloat(attr(start, "left"), 0),
					width:  parseFloat(attr(start, "width"), 0),
					height: parseFloat(attr(start, "height"), 0),
					image:  true,
					src:    attr(start, "src"),
				})
			}

			if err := dec.Skip(); err != nil {
				return nil, fmt.Errorf("skip image: %w", err)
			}
		}
	}

	if inPage {
		cur.lines = groupLines(runs)
		pages = append(pages, cur)
	}

	return pages, nil
}

func stripDoctype(data []byte) []byte {
	// pdftohtml names a DTD that is not shipped next to the XML.
	lower := bytes.ToLower(data)
	start := bytes.Index(lower, []byte("<!doctype"))
	if start < 0 {
		return data
	}

	end := bytes.IndexByte(data[start:], '>')
	if end < 0 {
		return data
	}

	out := make([]byte, 0, len(data)-(end+1))
	out = append(out, data[:start]...)
	out = append(out, data[start+end+1:]...)

	return out
}

func readText(dec *xml.Decoder, start xml.StartElement, fonts map[string]fontSpec) (run, error) {
	spans, err := readSpans(dec, false, false, "")
	if err != nil {
		return run{}, fmt.Errorf("read text: %w", err)
	}

	font := fonts[attr(start, "font")]
	plain, rich, allBold, allItalic := compose(spans)
	family := strings.ToLower(font.family)

	if !hasInlineMarkup(spans) {
		if strings.Contains(family, "bold") {
			allBold = true
		}

		if strings.Contains(family, "italic") || strings.Contains(family, "oblique") {
			allItalic = true
		}

		rich = escapeMarkdown(plain)
	}

	size := font.size
	if size <= 0 {
		size = 12
	}

	height := parseFloat(attr(start, "height"), size)

	return run{
		top:       parseFloat(attr(start, "top"), 0),
		left:      parseFloat(attr(start, "left"), 0),
		width:     parseFloat(attr(start, "width"), 0),
		height:    height,
		size:      size,
		plain:     plain,
		rich:      rich,
		allBold:   allBold,
		allItalic: allItalic,
	}, nil
}

func readSpans(dec *xml.Decoder, bold, italic bool, href string) ([]span, error) {
	var spans []span

	for {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}

		switch t := tok.(type) {
		case xml.StartElement:
			nextBold, nextItalic, nextHref := bold, italic, href
			switch strings.ToLower(t.Name.Local) {
			case "b":
				nextBold = true
			case "i":
				nextItalic = true
			case "a":
				if value := attr(t, "href"); value != "" {
					nextHref = value
				}
			}

			inner, err := readSpans(dec, nextBold, nextItalic, nextHref)
			if err != nil {
				return nil, err
			}

			spans = append(spans, inner...)
		case xml.EndElement:
			return spans, nil
		case xml.CharData:
			if len(t) == 0 {
				continue
			}

			spans = append(spans, span{text: string(t), bold: bold, italic: italic, href: href})
		}
	}
}

func hasInlineMarkup(spans []span) bool {
	seenBold, seenRegular := false, false

	for _, sp := range spans {
		if strings.TrimSpace(sp.text) == "" {
			continue
		}

		if sp.href != "" || (sp.italic && !sp.bold) {
			return true
		}

		if sp.bold {
			seenBold = true
		} else {
			seenRegular = true
		}
	}

	return seenBold && seenRegular
}

func compose(spans []span) (plain, rich string, allBold, allItalic bool) {
	allBold, allItalic = true, true
	any := false
	var plainB, richB strings.Builder

	for _, sp := range spans {
		if sp.text == "" {
			continue
		}

		any = true
		plainB.WriteString(sp.text)
		piece := escapeMarkdown(sp.text)

		switch {
		case sp.href != "":
			richB.WriteByte('[')
			richB.WriteString(piece)
			richB.WriteString("](")
			richB.WriteString(escapeLink(sp.href))
			richB.WriteByte(')')
			allBold = false
			allItalic = false
		case sp.bold && sp.italic:
			richB.WriteString("***")
			richB.WriteString(piece)
			richB.WriteString("***")
		case sp.bold:
			richB.WriteString("**")
			richB.WriteString(piece)
			richB.WriteString("**")
			allItalic = false
		case sp.italic:
			richB.WriteByte('*')
			richB.WriteString(piece)
			richB.WriteByte('*')
			allBold = false
		default:
			richB.WriteString(piece)
			allBold = false
			allItalic = false
		}
	}

	if !any {
		return "", "", false, false
	}

	plain = plainB.String()
	if allBold || allItalic {
		return plain, escapeMarkdown(plain), allBold, allItalic
	}

	return plain, richB.String(), false, false
}

func groupLines(runs []run) []line {
	sort.SliceStable(runs, func(i, j int) bool {
		if math.Abs(runs[i].top-runs[j].top) > 1 {
			return runs[i].top < runs[j].top
		}

		return runs[i].left < runs[j].left
	})

	var lines []line

	for _, item := range runs {
		if item.image || len(lines) == 0 || lines[len(lines)-1].image || !overlaps(lines[len(lines)-1], item) {
			lines = append(lines, newLine(item))

			continue
		}

		lines[len(lines)-1].absorb(item)
	}

	for i := range lines {
		lines[i].buildSegments()
	}

	return lines
}

func newLine(item run) line {
	return line{
		top:       item.top,
		left:      item.left,
		width:     item.width,
		height:    item.height,
		size:      item.size,
		plain:     item.plain,
		rich:      item.rich,
		allBold:   item.allBold,
		allItalic: item.allItalic,
		image:     item.image,
		src:       item.src,
		items:     []run{item},
	}
}

func (l *line) absorb(item run) {
	top := math.Min(l.top, item.top)
	bottom := math.Max(l.bottom(), item.bottom())
	left := math.Min(l.left, item.left)
	right := math.Max(l.right(), item.right())
	l.top = top
	l.height = bottom - top
	l.left = left
	l.width = right - left
	if item.size > l.size {
		l.size = item.size
	}

	l.items = append(l.items, item)
}

func overlaps(l line, item run) bool {
	if l.height <= 0 || item.height <= 0 {
		return math.Abs(l.top-item.top) <= 2
	}

	top := math.Max(l.top, item.top)
	bottom := math.Min(l.bottom(), item.bottom())
	overlap := bottom - top
	smaller := math.Min(l.height, item.height)

	return overlap >= smaller*0.45
}

func (l *line) buildSegments() {
	if l.image {
		item := l.items[0]
		l.segments = []segment{{
			left: item.left, width: item.width, size: item.size,
			image: true, src: item.src,
		}}

		return
	}

	items := append([]run(nil), l.items...)
	sort.SliceStable(items, func(i, j int) bool { return items[i].left < items[j].left })

	var groups [][]run
	var current []run

	for _, item := range items {
		if len(current) == 0 {
			current = []run{item}

			continue
		}

		prev := current[len(current)-1]
		gap := item.left - prev.right()
		if gap > math.Max(l.size*2.2, 18) {
			groups = append(groups, current)
			current = []run{item}

			continue
		}

		current = append(current, item)
	}

	if len(current) > 0 {
		groups = append(groups, current)
	}

	l.segments = make([]segment, 0, len(groups))
	for _, group := range groups {
		l.segments = append(l.segments, segmentFromRuns(group))
	}

	l.applySegments()
}

func segmentFromRuns(items []run) segment {
	seg := segment{
		left:      items[0].left,
		width:     items[0].width,
		size:      items[0].size,
		allBold:   items[0].allBold,
		allItalic: items[0].allItalic,
	}

	var plain, rich strings.Builder
	plain.WriteString(strings.TrimSpace(items[0].plain))
	rich.WriteString(strings.TrimSpace(items[0].rich))
	prevRight := items[0].right()

	for _, item := range items[1:] {
		gap := item.left - prevRight
		joiner := ""
		if gap > item.size*0.12 {
			joiner = " "
		}

		plain.WriteString(joiner)
		plain.WriteString(strings.TrimSpace(item.plain))
		rich.WriteString(joiner)
		rich.WriteString(strings.TrimSpace(item.rich))

		if item.right() > seg.left+seg.width {
			seg.width = item.right() - seg.left
		}

		if item.size > seg.size {
			seg.size = item.size
		}

		seg.allBold = seg.allBold && item.allBold
		seg.allItalic = seg.allItalic && item.allItalic
		prevRight = item.right()
	}

	seg.plain = strings.TrimSpace(plain.String())
	seg.rich = strings.TrimSpace(rich.String())

	return seg
}

func (l *line) applySegments() {
	if len(l.segments) == 0 {
		return
	}

	if len(l.segments) == 1 {
		seg := l.segments[0]
		l.plain = seg.plain
		l.rich = seg.rich
		l.allBold = seg.allBold
		l.allItalic = seg.allItalic
		l.left = seg.left
		l.width = seg.width
		l.size = seg.size

		return
	}

	var plain strings.Builder
	l.allBold = true
	l.allItalic = true

	for i, seg := range l.segments {
		if i > 0 {
			plain.WriteByte(' ')
		}

		plain.WriteString(seg.plain)
		l.allBold = l.allBold && seg.allBold
		l.allItalic = l.allItalic && seg.allItalic
		if seg.size > l.size {
			l.size = seg.size
		}
	}

	l.plain = plain.String()
	l.rich = l.plain
}

type blockKind int

const (
	blockFlow blockKind = iota
	blockTable
	blockColumns
)

type block struct {
	kind        blockKind
	lines       []line
	left, right []line
}

func blocksFromLines(lines []line, pageWidth float64) []block {
	var blocks []block
	i := 0

	for i < len(lines) {
		if n := tableLen(lines[i:], pageWidth); n >= 2 {
			blocks = append(blocks, block{kind: blockTable, lines: cloneLines(lines[i : i+n])})
			i += n

			continue
		}

		if n, left, right, ok := takeColumns(lines[i:], pageWidth); ok {
			blocks = append(blocks, block{kind: blockColumns, left: left, right: right})
			i += n

			continue
		}

		j := i + 1
		for j < len(lines) && tableLen(lines[j:], pageWidth) < 2 && !columnStart(lines[j:], pageWidth) {
			j++
		}

		blocks = append(blocks, block{kind: blockFlow, lines: cloneLines(lines[i:j])})
		i = j
	}

	return blocks
}

func cloneLines(lines []line) []line {
	out := make([]line, len(lines))
	copy(out, lines)

	return out
}

func tableLen(lines []line, pageWidth float64) int {
	if len(lines) < 2 || len(lines[0].segments) < 2 || lines[0].image {
		return 0
	}

	n := 1
	for n < len(lines) {
		if lines[n].image || !sameGrid(lines[0], lines[n]) {
			break
		}

		gap := lines[n].top - lines[n-1].bottom()
		if gap > math.Max(14, lines[0].size*1.4) {
			break
		}

		n++
	}

	if n < 2 || looksLikeColumns(lines[:n], pageWidth) {
		return 0
	}

	return n
}

func sameGrid(a, b line) bool {
	if len(a.segments) != len(b.segments) || len(a.segments) < 2 {
		return false
	}

	for i := range a.segments {
		if math.Abs(a.segments[i].left-b.segments[i].left) > 12 {
			return false
		}
	}

	return true
}

// looksLikeColumns keeps a wide two-side prose gutter out of the table
// path. Short cells stay a table even when the gap is large.
func looksLikeColumns(lines []line, pageWidth float64) bool {
	if len(lines) == 0 || len(lines[0].segments) != 2 {
		return false
	}

	var gaps []float64
	words, segs := 0, 0

	for _, ln := range lines {
		if len(ln.segments) != 2 {
			continue
		}

		gap := ln.segments[1].left - (ln.segments[0].left + ln.segments[0].width)
		gaps = append(gaps, gap)
		words += len(strings.Fields(ln.segments[0].plain)) + len(strings.Fields(ln.segments[1].plain))
		segs += 2
	}

	if len(gaps) == 0 || segs == 0 {
		return false
	}

	sort.Float64s(gaps)
	median := gaps[len(gaps)/2]
	avgWords := float64(words) / float64(segs)

	return median >= 48 && median >= pageWidth*0.12 && avgWords >= 4
}

type gutter struct {
	x   float64
	gap float64
}

func columnStart(lines []line, pageWidth float64) bool {
	_, left, right, ok := takeColumns(lines, pageWidth)

	return ok && len(left) > 0 && len(right) > 0
}

func takeColumns(lines []line, pageWidth float64) (n int, left, right []line, ok bool) {
	if len(lines) == 0 || len(lines[0].segments) >= 3 || lines[0].image {
		return 0, nil, nil, false
	}

	window := lines
	if len(window) > 40 {
		window = window[:40]
	}

	gut, found := findGutter(window, pageWidth)
	if !found || lineCrosses(lines[0], gut) {
		return 0, nil, nil, false
	}

	if !lineInvolves(lines[0], gut) {
		if len(lines) < 2 || !lineUsesBoth(lines[1], gut) || lines[1].top-lines[0].bottom() > 24 {
			return 0, nil, nil, false
		}
	}

	buf := make([]line, 0, len(lines))
	for _, ln := range lines {
		if len(ln.segments) >= 3 {
			break
		}

		if len(buf) > 0 {
			if lineCrosses(ln, gut) {
				break
			}

			if ln.top-buf[len(buf)-1].bottom() > 36 && !lineInvolves(ln, gut) {
				break
			}
		}

		buf = append(buf, ln)
	}

	if len(buf) < 2 {
		return 0, nil, nil, false
	}

	left, right = splitColumns(buf, gut)
	if len(left) == 0 || len(right) == 0 {
		return 0, nil, nil, false
	}

	return len(buf), left, right, true
}

func findGutter(lines []line, pageWidth float64) (gutter, bool) {
	type cluster struct {
		xs   []float64
		gaps []float64
	}

	clusters := map[int]*cluster{}

	for _, ln := range lines {
		for i := 0; i+1 < len(ln.segments); i++ {
			gap := ln.segments[i+1].left - (ln.segments[i].left + ln.segments[i].width)
			if gap < 36 {
				continue
			}

			x := ln.segments[i+1].left
			key := int(math.Round(x / 10))
			c := clusters[key]
			if c == nil {
				c = &cluster{}
				clusters[key] = c
			}

			c.xs = append(c.xs, x)
			c.gaps = append(c.gaps, gap)
		}
	}

	var best gutter
	bestScore := 0.0

	for _, c := range clusters {
		if len(c.gaps) < 2 {
			continue
		}

		sort.Float64s(c.gaps)
		sort.Float64s(c.xs)
		medianGap := c.gaps[len(c.gaps)/2]
		if medianGap < 48 && medianGap < pageWidth*0.08 {
			continue
		}

		score := float64(len(c.gaps)) * medianGap
		if score > bestScore {
			bestScore = score
			best = gutter{x: c.xs[len(c.xs)/2], gap: medianGap}
		}
	}

	if bestScore == 0 {
		return gutter{}, false
	}

	return best, true
}

func lineCrosses(ln line, gut gutter) bool {
	// A column row's bounding box covers both sides. Only a single run
	// that starts in the left column and ends in the right one is a
	// full-width line, and that ends the column band.
	for _, seg := range ln.segments {
		if seg.left < gut.x-gut.gap*0.5 && seg.left+seg.width > gut.x+4 {
			return true
		}
	}

	return false
}

func lineUsesBoth(ln line, gut gutter) bool {
	hasLeft, hasRight := false, false

	for _, seg := range ln.segments {
		if seg.left+seg.width/2 < gut.x {
			hasLeft = true
		} else {
			hasRight = true
		}
	}

	return hasLeft && hasRight
}

func lineInvolves(ln line, gut gutter) bool {
	if lineUsesBoth(ln, gut) {
		return true
	}

	for _, seg := range ln.segments {
		if seg.left+seg.width*0.5 >= gut.x-4 {
			return true
		}
	}

	return false
}

func splitColumns(lines []line, gut gutter) (left, right []line) {
	for _, ln := range lines {
		var leftSeg, rightSeg []segment

		for _, seg := range ln.segments {
			if seg.left+seg.width/2 < gut.x {
				leftSeg = append(leftSeg, seg)
			} else {
				rightSeg = append(rightSeg, seg)
			}
		}

		if col, ok := lineFromSegments(ln, leftSeg); ok {
			left = append(left, col)
		}

		if col, ok := lineFromSegments(ln, rightSeg); ok {
			right = append(right, col)
		}
	}

	return left, right
}

func lineFromSegments(base line, segs []segment) (line, bool) {
	if len(segs) == 0 {
		return line{}, false
	}

	ln := base
	ln.items = nil
	ln.segments = segs
	ln.left = segs[0].left
	ln.width = segs[len(segs)-1].left + segs[len(segs)-1].width - segs[0].left
	ln.size = segs[0].size
	ln.image = segs[0].image
	ln.src = segs[0].src
	ln.allBold = segs[0].allBold
	ln.allItalic = segs[0].allItalic
	ln.plain = segs[0].plain
	ln.rich = segs[0].rich

	if len(segs) == 1 {
		return ln, true
	}

	var plain, rich strings.Builder
	for i, seg := range segs {
		if i > 0 {
			plain.WriteByte(' ')
			rich.WriteByte(' ')
		}

		plain.WriteString(seg.plain)
		rich.WriteString(seg.rich)
		ln.allBold = ln.allBold && seg.allBold
		ln.allItalic = ln.allItalic && seg.allItalic
		if seg.size > ln.size {
			ln.size = seg.size
		}
	}

	ln.plain = plain.String()
	ln.rich = rich.String()
	ln.image = false

	return ln, true
}

func renderBlocks(blocks []block, body float64, options markdown.Options, xmlDir, stem string, imageNum *int) (string, []imageJob, int) {
	parts := make([]string, 0, len(blocks))
	var images []imageJob
	letters := 0

	for _, blk := range blocks {
		switch blk.kind {
		case blockTable:
			parts = append(parts, renderTable(blk.lines, options.Flavour))
			for _, ln := range blk.lines {
				letters += countText(ln.plain)
			}
		case blockColumns:
			// Each side is one column, so its lines have a single segment
			// and this call does not split them again. A remaining gutter
			// on the right (a third column) still splits.
			left, leftImages, leftLetters := renderBlocks(blocksFromLines(blk.left, 10000), body, options, xmlDir, stem, imageNum)
			right, rightImages, rightLetters := renderBlocks(blocksFromLines(blk.right, 10000), body, options, xmlDir, stem, imageNum)
			if strings.TrimSpace(left) != "" {
				parts = append(parts, strings.TrimSpace(left))
			}

			if strings.TrimSpace(right) != "" {
				parts = append(parts, strings.TrimSpace(right))
			}

			images = append(images, leftImages...)
			images = append(images, rightImages...)
			letters += leftLetters + rightLetters
		case blockFlow:
			text, jobs, n := renderFlow(blk.lines, body, options, xmlDir, stem, imageNum)
			if strings.TrimSpace(text) != "" {
				parts = append(parts, strings.TrimSpace(text))
			}

			images = append(images, jobs...)
			letters += n
		}
	}

	return strings.Join(parts, "\n\n"), images, letters
}

func renderFlow(lines []line, body float64, options markdown.Options, xmlDir, stem string, imageNum *int) (string, []imageJob, int) {
	var (
		parts   []string
		para    []line
		items   []listItem
		images  []imageJob
		letters int
	)

	flushPara := func() {
		if len(para) == 0 {
			return
		}

		parts = append(parts, joinParagraph(para))
		para = nil
	}

	flushList := func() {
		if len(items) == 0 {
			return
		}

		parts = append(parts, renderList(items))
		items = nil
	}

	for _, ln := range lines {
		if ln.image {
			flushPara()
			flushList()

			if !options.ExtractImages {
				continue
			}

			job, md, ok := placeImage(ln, options.ImageDir, xmlDir, stem, imageNum)
			if !ok {
				continue
			}

			images = append(images, job)
			parts = append(parts, md)

			continue
		}

		letters += countText(ln.plain)

		if heading, ok := headingMarkdown(ln, body); ok {
			flushPara()
			flushList()
			parts = append(parts, heading)

			continue
		}

		if item, ok := asListItem(ln); ok {
			flushPara()
			// A bullet list and a numbered list are separate blocks even
			// when the numbered lines sit a little further right.
			if len(items) > 0 && items[0].ordered != item.ordered {
				flushList()
			}

			items = append(items, item)

			continue
		}

		if len(items) > 0 && continuesList(items[len(items)-1], ln) {
			items[len(items)-1].text = strings.TrimSpace(items[len(items)-1].text + " " + emitInline(ln))
			items[len(items)-1].bottom = ln.bottom()

			continue
		}

		flushList()

		if len(para) > 0 && !continuesParagraph(para[len(para)-1], ln) {
			flushPara()
		}

		para = append(para, ln)
	}

	flushPara()
	flushList()

	return strings.Join(parts, "\n\n"), images, letters
}

func continuesParagraph(prev, next line) bool {
	if prev.image || next.image || math.Abs(prev.size-next.size) > 0.6 || math.Abs(prev.left-next.left) > 14 {
		return false
	}

	return next.top-prev.bottom() <= math.Max(prev.size*0.85, 8)
}

func continuesList(item listItem, ln line) bool {
	if ln.image {
		return false
	}

	if _, ok := asListItem(ln); ok {
		return false
	}

	if ln.top-item.bottom > math.Max(ln.size*0.85, 8) {
		return false
	}

	return ln.left+2 >= item.left
}

func joinParagraph(lines []line) string {
	text := emitInline(lines[0])

	for _, ln := range lines[1:] {
		next := emitInline(ln)
		if strings.HasSuffix(text, "-") {
			r, _ := utf8.DecodeRuneInString(next)
			if unicode.IsLower(r) {
				text = strings.TrimSuffix(text, "-") + next

				continue
			}
		}

		text += " " + next
	}

	return strings.TrimSpace(text)
}

func emitInline(ln line) string {
	rich := strings.TrimSpace(ln.rich)
	if rich == "" {
		rich = escapeMarkdown(strings.TrimSpace(ln.plain))
	}

	switch {
	case ln.allBold && ln.allItalic:
		return "***" + rich + "***"
	case ln.allBold:
		return "**" + rich + "**"
	case ln.allItalic:
		return "*" + rich + "*"
	default:
		return rich
	}
}

func headingMarkdown(ln line, body float64) (string, bool) {
	plain := strings.TrimSpace(ln.plain)
	if plain == "" || ln.image || len(ln.segments) > 1 {
		return "", false
	}

	if _, ok := asListItem(ln); ok {
		return "", false
	}

	words := strings.Fields(plain)
	if len(words) == 0 || len(words) > 14 {
		return "", false
	}

	if body <= 0 {
		body = ln.size
	}

	ratio := ln.size / body
	if ratio < 1.15 && ln.size < body+2 {
		return "", false
	}

	level := 3
	switch {
	case ratio >= 1.8:
		level = 1
	case ratio >= 1.45:
		level = 2
	}

	return strings.Repeat("#", level) + " " + escapeMarkdown(plain), true
}

type listItem struct {
	ordered bool
	number  string
	text    string
	left    float64
	bottom  float64
}

var (
	unorderedItem = regexp.MustCompile(`^(?:[-*\x{2022}\x{25CF}\x{25AA}\x{00B7}])\s+(\S(?:.*\S)?)\s*$`)
	orderedItem   = regexp.MustCompile(`^(\d{1,4})[.)]\s+(\S(?:.*\S)?)\s*$`)
)

func asListItem(ln line) (listItem, bool) {
	plain := strings.TrimSpace(ln.plain)
	if plain == "" || ln.image || len(ln.segments) > 1 {
		return listItem{}, false
	}

	if m := orderedItem.FindStringSubmatch(plain); m != nil {
		return listItem{
			ordered: true,
			number:  m[1],
			text:    formatItemBody(ln, m[2]),
			left:    ln.left,
			bottom:  ln.bottom(),
		}, true
	}

	if m := unorderedItem.FindStringSubmatch(plain); m != nil {
		return listItem{
			text:   formatItemBody(ln, m[1]),
			left:   ln.left,
			bottom: ln.bottom(),
		}, true
	}

	return listItem{}, false
}

func formatItemBody(ln line, body string) string {
	text := escapeMarkdown(strings.TrimSpace(body))
	switch {
	case ln.allBold:
		return "**" + text + "**"
	case ln.allItalic:
		return "*" + text + "*"
	default:
		return text
	}
}

func renderList(items []listItem) string {
	if len(items) == 0 {
		return ""
	}

	base := items[0].left
	var b strings.Builder

	for i, item := range items {
		if i > 0 {
			b.WriteByte('\n')
		}

		level := max(int(math.Round((item.left-base)/18)), 0)

		if level > 3 {
			level = 3
		}

		b.WriteString(strings.Repeat("  ", level))
		if item.ordered {
			b.WriteString(item.number)
			b.WriteString(". ")
		} else {
			b.WriteString("- ")
		}

		b.WriteString(item.text)
	}

	return b.String()
}

func renderTable(lines []line, flavour markdown.Flavour) string {
	rows := make([][]string, 0, len(lines))
	width := 0

	for _, ln := range lines {
		row := make([]string, len(ln.segments))
		for i, seg := range ln.segments {
			row[i] = strings.TrimSpace(seg.plain)
		}

		if len(row) > width {
			width = len(row)
		}

		rows = append(rows, row)
	}

	for i := range rows {
		for len(rows[i]) < width {
			rows[i] = append(rows[i], "")
		}
	}

	if flavour == markdown.FlavourCommonMark {
		return renderHTMLTable(rows)
	}

	return renderGFMTable(rows)
}

func renderGFMTable(rows [][]string) string {
	var b strings.Builder

	writeRow := func(row []string) {
		b.WriteByte('|')
		for _, cell := range row {
			b.WriteByte(' ')
			b.WriteString(escapeTableCell(cell))
			b.WriteString(" |")
		}

		b.WriteByte('\n')
	}

	writeRow(rows[0])
	b.WriteByte('|')
	for range rows[0] {
		b.WriteString(" --- |")
	}

	b.WriteByte('\n')

	for _, row := range rows[1:] {
		writeRow(row)
	}

	return strings.TrimRight(b.String(), "\n")
}

func renderHTMLTable(rows [][]string) string {
	var b strings.Builder
	b.WriteString("<table>\n<thead>\n<tr>")

	for _, cell := range rows[0] {
		b.WriteString("<th>")
		b.WriteString(escapeHTML(cell))
		b.WriteString("</th>")
	}

	b.WriteString("</tr>\n</thead>\n<tbody>\n")

	for _, row := range rows[1:] {
		b.WriteString("<tr>")
		for _, cell := range row {
			b.WriteString("<td>")
			b.WriteString(escapeHTML(cell))
			b.WriteString("</td>")
		}

		b.WriteString("</tr>\n")
	}

	b.WriteString("</tbody>\n</table>")

	return b.String()
}

func escapeTableCell(value string) string {
	value = strings.ReplaceAll(value, "\n", " ")
	value = strings.ReplaceAll(value, "|", "\\|")

	return escapeMarkdown(strings.TrimSpace(value))
}

func escapeHTML(value string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;").Replace(value)
}

func escapeMarkdown(value string) string {
	return strings.NewReplacer(`\`, `\\`, "`", "\\`", "*", "\\*", "_", "\\_", "[", "\\[", "]", "\\]").Replace(value)
}

func escapeLink(value string) string {
	return strings.ReplaceAll(value, ")", "%29")
}

func placeImage(ln line, imageDir, xmlDir, stem string, imageNum *int) (imageJob, string, bool) {
	src := ln.src
	if src == "" && len(ln.segments) > 0 {
		src = ln.segments[0].src
	}

	if src == "" {
		return imageJob{}, "", false
	}

	if !filepath.IsAbs(src) {
		src = filepath.Join(xmlDir, src)
	}

	rel, err := filepath.Rel(xmlDir, filepath.Clean(src))
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return imageJob{}, "", false
	}

	*imageNum++
	ext := strings.ToLower(filepath.Ext(src))
	if ext == "" {
		ext = ".img"
	}

	name := fmt.Sprintf("%s-%d%s", stem, *imageNum, ext)

	return imageJob{src: src, dest: filepath.Join(imageDir, name)}, "![](" + name + ")", true
}

func bodySize(lines []line) float64 {
	counts := map[int]int{}

	for _, ln := range lines {
		if ln.image || ln.size <= 0 {
			continue
		}

		n := countText(ln.plain)
		if n == 0 {
			n = 1
		}

		counts[int(math.Round(ln.size*2))] += n
	}

	bestKey, best := 0, 0
	for key, n := range counts {
		if n > best || (n == best && (bestKey == 0 || key < bestKey)) {
			best = n
			bestKey = key
		}
	}

	if bestKey == 0 {
		return 12
	}

	return float64(bestKey) / 2
}

func documentStem(name string) string {
	stem := strings.TrimSuffix(filepath.Base(strings.TrimSpace(name)), filepath.Ext(name))
	var b strings.Builder

	for _, r := range stem {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '.' || r == '_' || r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}

	out := strings.Trim(b.String(), "-.")
	if out == "" {
		return "document"
	}

	return out
}

func attr(start xml.StartElement, name string) string {
	for _, a := range start.Attr {
		if a.Name.Local == name {
			return a.Value
		}
	}

	return ""
}

func parseFloat(value string, fallback float64) float64 {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}

	n, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return fallback
	}

	return n
}
