package pdfmarkdown

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gotenberg/gotenberg/v8/pkg/gotenberg"
	"github.com/gotenberg/gotenberg/v8/pkg/modules/api"
	"github.com/gotenberg/gotenberg/v8/pkg/modules/markdown"
)

const articleMarkdown = `# Quarterly Report

Revenue grew in the first half. The team shipped the conversion API on time.

- First milestone closed in March
- Second milestone closed in June

1. Review the figures
2. Publish the notes

---

## Left column

Alpha text stays on the left side of this page and should be read before the right side.

## Right column

Beta text stays on the right side and comes after the left column in reading order.

---

### Scores

| Name | Score | City |
| --- | --- | --- |
| Ada | 10 | Paris |
| Grace | 9 | London |
`

func TestPdfRegistered(t *testing.T) {
	for _, desc := range gotenberg.GetModuleDescriptors() {
		if desc.ID == "pdf" {
			return
		}
	}

	t.Fatal("module pdf is not registered")
}

func TestPdf_Extensions(t *testing.T) {
	got := strings.Join(new(Pdf).Extensions(), " ")
	if got != ".pdf" {
		t.Fatalf("extensions = %q", got)
	}
}

func TestPdf_ProvisionAndValidate(t *testing.T) {
	t.Run("missing binary path", func(t *testing.T) {
		t.Setenv(pdftohtmlBinEnv, "")

		engine := new(Pdf)
		err := engine.Provision(provisionContext(t, nil))
		if err == nil || !strings.Contains(err.Error(), pdftohtmlBinEnv) {
			t.Fatalf("error = %v, want %s", err, pdftohtmlBinEnv)
		}
	})

	t.Run("separator and binary", func(t *testing.T) {
		bin := filepath.Join(t.TempDir(), "pdftohtml")
		if err := os.WriteFile(bin, []byte{}, 0o700); err != nil {
			t.Fatalf("write bin: %v", err)
		}

		t.Setenv(pdftohtmlBinEnv, bin)

		engine := new(Pdf)
		err := engine.Provision(provisionContext(t, []string{"--pdf-markdown-page-separator", `\n***`}))
		if err != nil {
			t.Fatalf("provision: %v", err)
		}

		if engine.pageSeparator != "\n***" {
			t.Fatalf("separator = %q", engine.pageSeparator)
		}

		if err := engine.Validate(); err != nil {
			t.Fatalf("validate: %v", err)
		}

		missing := new(Pdf)
		missing.binPath = filepath.Join(t.TempDir(), "missing")
		err = missing.Validate()
		if err == nil || !strings.Contains(err.Error(), pdftohtmlBinEnv) {
			t.Fatalf("validate error = %v", err)
		}
	})
}

func TestPdf_DebugUsesBuildVersion(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "pdf"), []byte("pdftohtml version 25.03.0\n"), 0o600); err != nil {
		t.Fatalf("write version: %v", err)
	}

	t.Setenv(gotenberg.BuildVersionsDirPathEnvVar, dir)

	engine := &Pdf{binPath: "/nonexistent/pdftohtml", pageSeparator: defaultPageSeparator}
	if got := engine.Debug()["version"]; got != "pdftohtml version 25.03.0" {
		t.Fatalf("version = %v", got)
	}
}

func TestPdf_ConvertArticle(t *testing.T) {
	engine := testEngine(t)
	dir := t.TempDir()
	out := filepath.Join(dir, "out.md")

	err := engine.Convert(t.Context(), nil, filepath.Join("testdata", "article.pdf"), out, markdown.Options{
		Filename: "article.PDF",
		Flavour:  markdown.FlavourGFM,
	})
	if err != nil {
		t.Fatalf("convert: %v", err)
	}

	got, err := os.ReadFile(out) //nolint:gosec // out is a temp file this test created.
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	if string(got) != articleMarkdown {
		t.Fatalf("markdown = %q\nwant %q", got, articleMarkdown)
	}

	left := strings.Index(articleMarkdown, "be read before the right side.")
	right := strings.Index(articleMarkdown, "Beta text stays on the right")
	if left < 0 || right < 0 || left > right {
		t.Fatalf("column order left=%d right=%d", left, right)
	}
}

func TestPdf_ConvertCommonMarkTable(t *testing.T) {
	engine := testEngine(t)
	out := filepath.Join(t.TempDir(), "out.md")

	err := engine.Convert(t.Context(), nil, filepath.Join("testdata", "article.pdf"), out, markdown.Options{
		Filename: "article.pdf",
		Flavour:  markdown.FlavourCommonMark,
	})
	if err != nil {
		t.Fatalf("convert: %v", err)
	}

	got := readString(t, out)
	if !strings.Contains(got, "<table>") || strings.Contains(got, "| Ada |") {
		t.Fatalf("commonmark table = %s", got)
	}

	if !strings.Contains(got, "<td>Ada</td>") {
		t.Fatalf("missing cell: %s", got)
	}
}

func TestPdf_PageSeparator(t *testing.T) {
	engine := testEngine(t)
	engine.pageSeparator = "***"
	out := filepath.Join(t.TempDir(), "out.md")

	err := engine.Convert(t.Context(), nil, filepath.Join("testdata", "article.pdf"), out, markdown.Options{Filename: "article.pdf"})
	if err != nil {
		t.Fatalf("convert: %v", err)
	}

	got := readString(t, out)
	if !strings.Contains(got, "\n***\n") || strings.Contains(got, "\n---\n") {
		t.Fatalf("separator = %s", got)
	}

	engine.pageSeparator = ""
	out = filepath.Join(t.TempDir(), "bare.md")
	err = engine.Convert(t.Context(), nil, filepath.Join("testdata", "article.pdf"), out, markdown.Options{Filename: "article.pdf"})
	if err != nil {
		t.Fatalf("convert: %v", err)
	}

	got = readString(t, out)
	if strings.Contains(got, "\n---\n") || strings.Contains(got, "***") {
		t.Fatalf("empty separator still wrote a rule: %s", got)
	}
}

func TestPdf_ExtractImages(t *testing.T) {
	engine := testEngine(t)
	dir := t.TempDir()
	imageDir := filepath.Join(dir, "images")
	if err := os.Mkdir(imageDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	out := filepath.Join(dir, "out.md")
	err := engine.Convert(t.Context(), nil, filepath.Join("testdata", "image.pdf"), out, markdown.Options{
		Filename:      "photo.pdf",
		ExtractImages: true,
		ImageDir:      imageDir,
	})
	if err != nil {
		t.Fatalf("convert: %v", err)
	}

	got := readString(t, out)
	if !strings.Contains(got, "Caption above the image") || !strings.Contains(got, "![](photo-1.jpg)") {
		t.Fatalf("markdown = %s", got)
	}

	info, err := os.Stat(filepath.Join(imageDir, "photo-1.jpg"))
	if err != nil || info.Size() == 0 {
		t.Fatalf("image: %v size %v", err, info)
	}

	bareDir := filepath.Join(dir, "bare")
	if err := os.Mkdir(bareDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	bare := filepath.Join(dir, "bare.md")
	err = engine.Convert(t.Context(), nil, filepath.Join("testdata", "image.pdf"), bare, markdown.Options{
		Filename:      "photo.pdf",
		ExtractImages: false,
		ImageDir:      bareDir,
	})
	if err != nil {
		t.Fatalf("convert without images: %v", err)
	}

	if strings.Contains(readString(t, bare), "![](") {
		t.Fatal("wrote an image link with extractImages false")
	}

	entries, err := os.ReadDir(bareDir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("image dir = %v, %v", entries, err)
	}
}

func TestPdf_RejectsEncryptedAndScanned(t *testing.T) {
	for _, tc := range []struct {
		name     string
		file     string
		filename string
		want     error
		message  string
		bin      bool
	}{
		{name: "password", file: "encrypted.pdf", filename: "secret.pdf", want: ErrEncrypted, message: "secret.pdf' is encrypted"},
		{name: "owner password", file: "restricted.pdf", filename: "notes.pdf", want: ErrEncrypted, message: "notes.pdf' is encrypted"},
		{name: "scanned", file: "scanned.pdf", filename: "scan.pdf", want: ErrNoText, message: "scan.pdf' has no extractable text", bin: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			engine := &Pdf{binPath: "/nonexistent/pdftohtml", pageSeparator: defaultPageSeparator}
			if tc.bin {
				engine = testEngine(t)
			}

			dir := t.TempDir()
			out := filepath.Join(dir, "out.md")
			err := engine.Convert(t.Context(), nil, filepath.Join("testdata", tc.file), out, markdown.Options{Filename: tc.filename})
			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}

			// The route and the registry both wrap with %w. The client
			// message still has to survive that.
			wrapped := fmt.Errorf("convert '%s' to markdown: %w", tc.filename, fmt.Errorf("convert .pdf to markdown: %w", err))
			status, message := api.ParseError(wrapped)
			if status != 400 || !strings.Contains(message, tc.message) {
				t.Fatalf("status %d message %q", status, message)
			}

			if _, statErr := os.Stat(out); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("output exists: %v", statErr)
			}
		})
	}
}

func TestPdf_ConvertRejects(t *testing.T) {
	engine := &Pdf{binPath: "/nonexistent/pdftohtml", pageSeparator: defaultPageSeparator}

	err := engine.Convert(nil, nil, "testdata/article.pdf", filepath.Join(t.TempDir(), "out.md"), markdown.Options{Filename: "a.pdf"})
	if err == nil || !strings.Contains(err.Error(), "nil context") {
		t.Fatalf("nil context = %v", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	err = engine.Convert(ctx, nil, "testdata/article.pdf", filepath.Join(t.TempDir(), "out.md"), markdown.Options{Filename: "a.pdf"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled = %v", err)
	}

	err = engine.Convert(t.Context(), nil, "testdata/article.pdf", filepath.Join(t.TempDir(), "out.md"), markdown.Options{Filename: "notes.docx"})
	if !errors.Is(err, markdown.ErrFormatNotSupported) {
		t.Fatalf("extension = %v", err)
	}

	err = engine.Convert(t.Context(), nil, "testdata/article.pdf", filepath.Join(t.TempDir(), "out.md"), markdown.Options{
		Filename:      "a.pdf",
		ExtractImages: true,
	})
	if err == nil || !strings.Contains(err.Error(), "image directory") {
		t.Fatalf("image dir = %v", err)
	}
}

func provisionContext(t *testing.T, args []string) *gotenberg.Context {
	t.Helper()

	engine := new(Pdf)
	fs := engine.Descriptor().FlagSet
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil {
		t.Fatalf("parse flags: %v", err)
	}

	return gotenberg.NewContext(gotenberg.ParsedFlags{FlagSet: fs}, nil)
}

func testEngine(t *testing.T) *Pdf {
	t.Helper()

	bin := os.Getenv(pdftohtmlBinEnv)
	if bin == "" {
		var err error
		bin, err = exec.LookPath("pdftohtml")
		if err != nil {
			t.Skip("pdftohtml is not installed")
		}
	}

	return &Pdf{binPath: bin, pageSeparator: defaultPageSeparator}
}

func readString(t *testing.T, path string) string {
	t.Helper()

	got, err := os.ReadFile(path) //nolint:gosec // path is a temp file this test created.
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}

	return string(got)
}
