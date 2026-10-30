package pdfmarkdown

import (
	"strings"
	"testing"

	"github.com/gotenberg/gotenberg/v8/pkg/modules/markdown"
)

func TestRenderXML_Inline(t *testing.T) {
	const xml = `<?xml version="1.0" encoding="UTF-8"?>
<pdf2xml>
<page number="1" width="612" height="792">
<fontspec id="0" size="12" family="Helvetica"/>
<text top="10" left="72" width="40" height="11" font="0">con-</text>
<text top="24" left="72" width="40" height="11" font="0">vert</text>
<text top="60" left="72" width="140" height="11" font="0">Hello <b>world</b> today</text>
<text top="90" left="72" width="180" height="11" font="0">See the <a href="https://example.com/a)">site</a> now.</text>
<text top="120" left="72" width="80" height="11" font="0">2 * 3_4</text>
</page>
</pdf2xml>`

	got, err := renderXML([]byte(xml), t.TempDir(), markdown.Options{Flavour: markdown.FlavourGFM}, defaultPageSeparator)
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	want := strings.Join([]string{
		"convert",
		"",
		"Hello **world** today",
		"",
		"See the [site](https://example.com/a%29) now.",
		"",
		`2 \* 3\_4`,
		"",
	}, "\n")
	if got.markdown != want {
		t.Fatalf("markdown = %q, want %q", got.markdown, want)
	}

	if !got.hasText {
		t.Fatal("hasText = false")
	}
}

func TestRenderXML_EmptyPageHasNoText(t *testing.T) {
	const xml = `<?xml version="1.0" encoding="UTF-8"?>
<pdf2xml>
<page number="1" width="612" height="792">
</page>
</pdf2xml>`

	got, err := renderXML([]byte(xml), t.TempDir(), markdown.Options{}, "---")
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	if got.hasText || got.markdown != "" {
		t.Fatalf("empty page rendered %#v", got)
	}
}
