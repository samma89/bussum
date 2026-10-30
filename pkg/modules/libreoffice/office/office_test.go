package office

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gotenberg/gotenberg/v8/pkg/modules/markdown"
)

func TestOffice_DescriptorAndExtensions(t *testing.T) {
	engine := new(Office)

	if engine.Descriptor().ID != "office" {
		t.Fatalf("id = %q, want office", engine.Descriptor().ID)
	}

	got := engine.Extensions()
	want := []string{
		".docx", ".doc",
		".xlsx", ".xls",
		".pptx", ".ppt",
		".odt", ".ods", ".odp",
		".rtf",
	}

	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("extensions = %v, want %v", got, want)
	}

	fresh := engine.Descriptor().New()
	if fresh == nil {
		t.Fatal("New returned nil")
	}

	if _, ok := fresh.(*Office); !ok {
		t.Fatalf("New returned %T, want *Office", fresh)
	}
}

func TestOffice_Convert(t *testing.T) {
	t.Run("nil context", func(t *testing.T) {
		engine := &Office{exporter: &fakeExporter{html: "<p>Hi</p>"}}
		err := engine.Convert(nil, nil, "in.docx", "out.md", markdown.Options{Filename: "notes.docx"})
		if err == nil {
			t.Fatal("expected an error, got none")
		}
	})

	t.Run("not provisioned", func(t *testing.T) {
		err := new(Office).Convert(t.Context(), slog.New(slog.DiscardHandler), "in.docx", filepath.Join(t.TempDir(), "out.md"), markdown.Options{Filename: "notes.docx"})
		if err == nil || !strings.Contains(err.Error(), "not provisioned") {
			t.Fatalf("error = %v, want not provisioned", err)
		}
	})

	t.Run("cancelled before export", func(t *testing.T) {
		exporter := &fakeExporter{html: "<p>Hi</p>"}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		err := (&Office{exporter: exporter}).Convert(ctx, slog.New(slog.DiscardHandler), "in.docx", filepath.Join(t.TempDir(), "out.md"), markdown.Options{Filename: "notes.docx"})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context.Canceled", err)
		}

		if exporter.calls != 0 {
			t.Fatalf("exporter calls = %d, want 0", exporter.calls)
		}
	})

	t.Run("unsupported extension", func(t *testing.T) {
		exporter := &fakeExporter{html: "<p>Hi</p>"}
		err := (&Office{exporter: exporter}).Convert(t.Context(), slog.New(slog.DiscardHandler), "blob", filepath.Join(t.TempDir(), "out.md"), markdown.Options{Filename: "notes.txt"})
		if !errors.Is(err, markdown.ErrFormatNotSupported) {
			t.Fatalf("error = %v, want ErrFormatNotSupported", err)
		}

		if exporter.calls != 0 {
			t.Fatalf("exporter calls = %d, want 0", exporter.calls)
		}
	})

	t.Run("filename selects the extension", func(t *testing.T) {
		dir := t.TempDir()
		exporter := &fakeExporter{html: "<h1>FromExport</h1>"}
		output := filepath.Join(dir, "out.md")
		input := filepath.Join(dir, "blob")

		err := os.WriteFile(input, []byte("not a docx"), 0o600)
		if err != nil {
			t.Fatalf("write input: %v", err)
		}

		err = (&Office{exporter: exporter}).Convert(t.Context(), nil, input, output, markdown.Options{Filename: "NOTES.DOCX"})
		if err != nil {
			t.Fatalf("convert: %v", err)
		}

		if exporter.input != input {
			t.Fatalf("exporter input = %q, want %q", exporter.input, input)
		}

		got := readString(t, output)
		requireContains(t, got, "# FromExport")
	})

	t.Run("exporter failure", func(t *testing.T) {
		exporter := &fakeExporter{err: errors.New("uno failed")}
		output := filepath.Join(t.TempDir(), "out.md")
		err := (&Office{exporter: exporter}).Convert(t.Context(), slog.New(slog.DiscardHandler), "in.docx", output, markdown.Options{Filename: "notes.docx"})
		if err == nil || !strings.Contains(err.Error(), "uno failed") {
			t.Fatalf("error = %v, want uno failed", err)
		}

		if _, statErr := os.Stat(output); !os.IsNotExist(statErr) {
			t.Fatalf("output stat = %v, want not exist", statErr)
		}
	})

	t.Run("bad flavour", func(t *testing.T) {
		exporter := &fakeExporter{html: "<p>Hi</p>"}
		err := (&Office{exporter: exporter}).Convert(t.Context(), slog.New(slog.DiscardHandler), "in.docx", filepath.Join(t.TempDir(), "out.md"), markdown.Options{
			Filename: "notes.docx",
			Flavour:  "latex",
		})
		if err == nil || !strings.Contains(err.Error(), "commonmark") {
			t.Fatalf("error = %v, want the flavour values", err)
		}

		if exporter.calls != 0 {
			t.Fatalf("exporter calls = %d, want 0", exporter.calls)
		}
	})

	t.Run("extract images requires a directory", func(t *testing.T) {
		exporter := &fakeExporter{html: "<p>Hi</p>"}
		err := (&Office{exporter: exporter}).Convert(t.Context(), slog.New(slog.DiscardHandler), "in.docx", filepath.Join(t.TempDir(), "out.md"), markdown.Options{
			Filename:      "notes.docx",
			ExtractImages: true,
		})
		if err == nil || !strings.Contains(err.Error(), "image directory") {
			t.Fatalf("error = %v, want image directory", err)
		}
	})
}

func TestMarkdownFromExport_Writer(t *testing.T) {
	imageDir := t.TempDir()
	md := exportFixture(t, "testdata/writer", markdown.Options{
		Flavour:       markdown.FlavourGFM,
		ExtractImages: true,
		ImageDir:      imageDir,
		Filename:      "notes.docx",
	}, kindText, nil)

	requireContains(t, md,
		"# AlphaHeading",
		"[CharlieLink](https://example.com/charlie)",
		"**BoldWord**",
		"*ItalicWord*",
		"- BravoBullet",
		"- [x] DoneItem",
		"- [ ] OpenItem",
		"- [ ] BoxItem",
		"~~FoxtrotStrike~~",
		"DeltaCell",
		"EchoCell",
		"![Diagram](notes-1.png)",
		"![DiagramTwo](notes-2.png)",
	)

	if !strings.Contains(md, "|") {
		t.Fatalf("gfm table missing pipes\n%s", md)
	}

	source := readBytes(t, "testdata/writer/diagram.png")
	for _, name := range []string{"notes-1.png", "notes-2.png"} {
		got := readBytes(t, filepath.Join(imageDir, name))
		if !bytes.Equal(got, source) {
			t.Fatalf("%s bytes differ from diagram.png", name)
		}
	}

	entries, err := os.ReadDir(imageDir)
	if err != nil {
		t.Fatalf("read image dir: %v", err)
	}

	if len(entries) != 2 {
		t.Fatalf("image count = %d, want 2", len(entries))
	}
}

func TestMarkdownFromExport_WriterCommonMark(t *testing.T) {
	imageDir := t.TempDir()
	md := exportFixture(t, "testdata/writer", markdown.Options{
		Flavour:       markdown.FlavourCommonMark,
		ExtractImages: false,
		ImageDir:      imageDir,
		Filename:      "notes.docx",
	}, kindText, nil)

	requireContains(t, md, "# AlphaHeading", "BravoBullet", "CharlieLink", "FoxtrotStrike", "DeltaCell", "DoneItem", "Diagram")
	requireNotContains(t, md, "~~", "- [x]", "- [ ]", "notes-1.png", "diagram.png", "| ---", "|---")

	entries, err := os.ReadDir(imageDir)
	if err != nil {
		t.Fatalf("read image dir: %v", err)
	}

	if len(entries) != 0 {
		t.Fatalf("image count = %d, want 0 when extractImages is false", len(entries))
	}
}

func TestMarkdownFromExport_Calc(t *testing.T) {
	md := exportFixture(t, "testdata/calc", markdown.Options{
		Flavour:  markdown.FlavourGFM,
		Filename: "book.xlsx",
	}, kindSheet, []string{"Fruit", "Totals"})

	requireContains(t, md, "## Fruit", "Apple", "## Totals", "Count")

	if strings.Count(md, "Fruit") != 1 || strings.Count(md, "Totals") != 1 {
		t.Fatalf("sheet headings duplicated\n%s", md)
	}
}

func TestMarkdownFromExport_CalcKeepsExistingHeading(t *testing.T) {
	md := exportFixture(t, "testdata/calc-headings", markdown.Options{
		Flavour:  markdown.FlavourGFM,
		Filename: "book.xlsx",
	}, kindSheet, []string{"Ignored"})

	requireContains(t, md, "# Fruit", "Apple")
	requireNotContains(t, md, "Ignored", "## Fruit")

	if strings.Count(md, "Fruit") != 1 {
		t.Fatalf("Fruit count = %d, want 1\n%s", strings.Count(md, "Fruit"), md)
	}
}

func TestMarkdownFromExport_SheetFallbackNames(t *testing.T) {
	dir := t.TempDir()
	err := os.WriteFile(filepath.Join(dir, "document.html"), []byte(`<html><body>
<table><tr><td>Only</td></tr></table>
<table><tr><td>Also</td></tr></table>
</body></html>`), 0o600)
	if err != nil {
		t.Fatalf("write html: %v", err)
	}

	md := mustMarkdown(t, dir, markdown.Options{Flavour: markdown.FlavourGFM, Filename: "book.xlsx"}, kindSheet, nil)
	requireContains(t, md, "## Sheet 1", "Only", "## Sheet 2", "Also")
}

func TestMarkdownFromExport_SheetFiles(t *testing.T) {
	dir := t.TempDir()
	err := os.WriteFile(filepath.Join(dir, "document.html"), []byte(`<html><body><table><tr><td>Apple</td></tr></table></body></html>`), 0o600)
	if err != nil {
		t.Fatalf("write first sheet: %v", err)
	}

	err = os.WriteFile(filepath.Join(dir, "document_2.html"), []byte(`<html><body><table><tr><td>Nine</td></tr></table></body></html>`), 0o600)
	if err != nil {
		t.Fatalf("write second sheet: %v", err)
	}

	md := mustMarkdown(t, dir, markdown.Options{Flavour: markdown.FlavourGFM, Filename: "book.xlsx"}, kindSheet, []string{"Fruit", "Totals"})
	requireContains(t, md, "## Fruit", "Apple", "## Totals", "Nine")
}

func TestMarkdownFromExport_Slides(t *testing.T) {
	imageDir := t.TempDir()
	md := exportFixture(t, "testdata/impress", markdown.Options{
		Flavour:       markdown.FlavourGFM,
		ExtractImages: true,
		ImageDir:      imageDir,
		Filename:      "my deck.pptx",
	}, kindSlides, nil)

	requireContains(t, md, "## SlideOne", "FirstPoint", "## SlideTwo", "SecondPoint", "![Pixel](my-deck-1.png)")
	requireNotContains(t, md, "FrameShell", "ShouldNotAppear", "OutlineSkip")

	if strings.Count(md, "SlideOne") != 1 || strings.Count(md, "SlideTwo") != 1 {
		t.Fatalf("slide titles duplicated\n%s", md)
	}

	source := readBytes(t, "testdata/impress/pixel.png")
	got := readBytes(t, filepath.Join(imageDir, "my-deck-1.png"))
	if !bytes.Equal(got, source) {
		t.Fatal("extracted slide image bytes differ")
	}
}

func TestMarkdownFromExport_Images(t *testing.T) {
	t.Run("remote url stays remote", func(t *testing.T) {
		dir := writeHTML(t, `<p><img src="https://example.com/a.png" alt="Remote"></p>`)
		imageDir := t.TempDir()
		md := mustMarkdown(t, dir, markdown.Options{
			Flavour:       markdown.FlavourGFM,
			ExtractImages: true,
			ImageDir:      imageDir,
			Filename:      "notes.docx",
		}, kindText, nil)

		requireContains(t, md, "https://example.com/a.png", "Remote")

		entries, err := os.ReadDir(imageDir)
		if err != nil {
			t.Fatalf("read image dir: %v", err)
		}

		if len(entries) != 0 {
			t.Fatalf("downloaded a remote image: %d files", len(entries))
		}
	})

	t.Run("data uri", func(t *testing.T) {
		const png = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="
		dir := writeHTML(t, `<p><img src="data:image/png;base64,`+png+`" alt="Dot"></p>`)
		imageDir := t.TempDir()
		md := mustMarkdown(t, dir, markdown.Options{
			Flavour:       markdown.FlavourGFM,
			ExtractImages: true,
			ImageDir:      imageDir,
			Filename:      "notes.docx",
		}, kindText, nil)

		requireContains(t, md, "![Dot](notes-1.png)")
		requireNotContains(t, md, "data:image")

		info, err := os.Stat(filepath.Join(imageDir, "notes-1.png"))
		if err != nil {
			t.Fatalf("stat image: %v", err)
		}

		if info.Size() == 0 {
			t.Fatal("data uri image is empty")
		}
	})

	t.Run("path escape keeps alt text", func(t *testing.T) {
		dir := t.TempDir()
		secret := filepath.Join(t.TempDir(), "secret.png")
		err := os.WriteFile(secret, []byte("secret-bytes"), 0o600)
		if err != nil {
			t.Fatalf("write secret: %v", err)
		}

		err = os.WriteFile(filepath.Join(dir, "document.html"), []byte(`<p><img src="../secret.png" alt="Hidden"></p>`), 0o600)
		if err != nil {
			t.Fatalf("write html: %v", err)
		}

		imageDir := t.TempDir()
		md := mustMarkdown(t, dir, markdown.Options{
			Flavour:       markdown.FlavourGFM,
			ExtractImages: true,
			ImageDir:      imageDir,
			Filename:      "notes.docx",
		}, kindText, nil)

		requireContains(t, md, "Hidden")
		requireNotContains(t, md, "secret-bytes", "notes-1.png")

		entries, err := os.ReadDir(imageDir)
		if err != nil {
			t.Fatalf("read image dir: %v", err)
		}

		if len(entries) != 0 {
			t.Fatalf("escaped image was copied: %d files", len(entries))
		}
	})
}

func TestDocumentStem(t *testing.T) {
	for _, tc := range []struct {
		filename string
		want     string
	}{
		{filename: "notes.docx", want: "notes"},
		{filename: "my notes.docx", want: "my-notes"},
		{filename: "../../etc/passwd.docx", want: "passwd"},
		{filename: "", want: "document"},
		{filename: "...", want: "document"},
	} {
		t.Run(tc.filename, func(t *testing.T) {
			got := documentStem(tc.filename)
			if got != tc.want {
				t.Fatalf("stem = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestOfficeFixtures(t *testing.T) {
	for _, tc := range []struct {
		name   string
		needle string
	}{
		{name: "office-notes.docx", needle: "AlphaHeading"},
		{name: "office-book.xlsx", needle: "Apple"},
		{name: "office-deck.pptx", needle: "SlideOne"},
		{name: "office-notes.odt", needle: "OdtHeading"},
		{name: "office-book.ods", needle: "OdsApple"},
		{name: "office-deck.odp", needle: "OdpSlide"},
		{name: "office-notes.rtf", needle: "RtfHeading"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := integrationFixture(tc.name)
			body := fixtureText(t, path)
			if !strings.Contains(body, tc.needle) {
				t.Fatalf("%s does not contain %q", tc.name, tc.needle)
			}

			ext := strings.ToLower(filepath.Ext(tc.name))
			if !new(Office).supports(ext) {
				t.Fatalf("engine does not claim %s", ext)
			}
		})
	}
}

func TestSheetNames(t *testing.T) {
	names, err := sheetNames(integrationFixture("office-book.xlsx"), ".xlsx")
	if err != nil {
		t.Fatalf("xlsx names: %v", err)
	}

	if strings.Join(names, ",") != "Fruit,Totals" {
		t.Fatalf("xlsx names = %v, want Fruit, Totals", names)
	}

	names, err = sheetNames(integrationFixture("office-book.ods"), ".ods")
	if err != nil {
		t.Fatalf("ods names: %v", err)
	}

	if strings.Join(names, ",") != "Fruit,Totals" {
		t.Fatalf("ods names = %v, want Fruit, Totals", names)
	}

	names, err = sheetNames(integrationFixture("office-notes.docx"), ".docx")
	if err != nil {
		t.Fatalf("docx names: %v", err)
	}

	if len(names) != 0 {
		t.Fatalf("docx names = %v, want none", names)
	}

	names, err = sheetNames(integrationFixture("office-notes.rtf"), ".rtf")
	if err != nil {
		t.Fatalf("rtf names: %v", err)
	}

	if len(names) != 0 {
		t.Fatalf("rtf names = %v, want none", names)
	}
}

type fakeExporter struct {
	html  string
	extra map[string][]byte
	err   error
	input string
	calls int
}

func (f *fakeExporter) Html(ctx context.Context, _ *slog.Logger, inputPath, outputPath string) error {
	f.calls++
	f.input = inputPath

	if err := ctx.Err(); err != nil {
		return err
	}

	if f.err != nil {
		return f.err
	}

	err := os.MkdirAll(filepath.Dir(outputPath), 0o700)
	if err != nil {
		return err
	}

	err = os.WriteFile(outputPath, []byte(f.html), 0o600)
	if err != nil {
		return err
	}

	for name, content := range f.extra {
		path := filepath.Join(filepath.Dir(outputPath), name)
		err = os.MkdirAll(filepath.Dir(path), 0o700)
		if err != nil {
			return err
		}

		err = os.WriteFile(path, content, 0o600)
		if err != nil {
			return err
		}
	}

	return nil
}

func exportFixture(t *testing.T, dir string, options markdown.Options, kind docKind, names []string) string {
	t.Helper()

	return mustMarkdown(t, dir, options, kind, names)
}

func mustMarkdown(t *testing.T, dir string, options markdown.Options, kind docKind, names []string) string {
	t.Helper()

	options, err := normalizeOptions(options)
	if err != nil {
		t.Fatalf("options: %v", err)
	}

	out, err := markdownFromExport(t.Context(), dir, filepath.Join(dir, "document.html"), kind, names, options)
	if err != nil {
		t.Fatalf("markdown: %v", err)
	}

	return string(out)
}

func writeHTML(t *testing.T, body string) string {
	t.Helper()

	dir := t.TempDir()
	err := os.WriteFile(filepath.Join(dir, "document.html"), []byte("<html><body>"+body+"</body></html>"), 0o600)
	if err != nil {
		t.Fatalf("write html: %v", err)
	}

	return dir
}

func integrationFixture(name string) string {
	return filepath.Join("..", "..", "..", "..", "test", "integration", "testdata", "markdown", name)
}

func fixtureText(t *testing.T, path string) string {
	t.Helper()

	if strings.HasSuffix(strings.ToLower(path), ".rtf") {
		return string(readBytes(t, path))
	}

	reader, err := zip.OpenReader(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}

	defer func() {
		_ = reader.Close()
	}()

	var buf strings.Builder

	for _, file := range reader.File {
		if file.FileInfo().IsDir() {
			continue
		}

		rc, err := file.Open()
		if err != nil {
			t.Fatalf("open %s in %s: %v", file.Name, path, err)
		}

		body, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			t.Fatalf("read %s in %s: %v", file.Name, path, err)
		}

		buf.Write(body)
		buf.WriteByte('\n')
	}

	return buf.String()
}

func readString(t *testing.T, path string) string {
	t.Helper()

	return string(readBytes(t, path))
}

func readBytes(t *testing.T, path string) []byte {
	t.Helper()

	body, err := os.ReadFile(path) //nolint:gosec // test fixture path
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}

	return body
}

func requireContains(t *testing.T, md string, parts ...string) {
	t.Helper()

	for _, part := range parts {
		if !strings.Contains(md, part) {
			t.Fatalf("markdown missing %q\n%s", part, md)
		}
	}
}

func requireNotContains(t *testing.T, md string, parts ...string) {
	t.Helper()

	for _, part := range parts {
		if strings.Contains(md, part) {
			t.Fatalf("markdown unexpectedly contains %q\n%s", part, md)
		}
	}
}
