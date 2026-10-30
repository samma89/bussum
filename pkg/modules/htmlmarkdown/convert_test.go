package htmlmarkdown

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gotenberg/gotenberg/v8/pkg/gotenberg"
	"github.com/gotenberg/gotenberg/v8/pkg/modules/markdown"
)

func TestModuleRegistered(t *testing.T) {
	for _, desc := range gotenberg.GetModuleDescriptors() {
		if desc.ID == "html" {
			return
		}
	}

	t.Fatal("html module is not registered")
}

func TestHTML_Extensions(t *testing.T) {
	got := new(HTML).Extensions()
	want := []string{".html", ".htm"}

	if len(got) != len(want) {
		t.Fatalf("extensions = %v, want %v", got, want)
	}

	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("extensions = %v, want %v", got, want)
		}
	}
}

func TestConvertHTML_Fixtures(t *testing.T) {
	for _, tc := range []struct {
		name    string
		file    string
		flavour markdown.Flavour
		want    []string
		absent  []string
	}{
		{
			name:    "gfm article",
			file:    "article.html",
			flavour: markdown.FlavourGFM,
			want: []string{
				"# HTML title",
				"## Section",
				"**bold**",
				"[link](https://example.com/docs)",
				"nested",
				"first",
				"> quoted words",
				"```go",
				`fmt.Println("hi")`,
				"| Name |",
				"| Ada",
				"~~gone~~",
				"<https://example.com>",
				"![diagram](https://example.com/diagram.png)",
				"Fallback text",
			},
			absent: []string{
				"Secret nav",
				"Also secret",
				"from-script",
				"color: red",
				"Ignored title",
				"secret-comment",
				"<!--THE END-->",
			},
		},
		{
			name:    "commonmark drops gfm syntax",
			file:    "article.html",
			flavour: markdown.FlavourCommonMark,
			want: []string{
				"# HTML title",
				"[link](https://example.com/docs)",
				"[https://example.com](https://example.com)",
				"Fallback text",
			},
			absent: []string{
				"~~gone~~",
				"<https://example.com>",
				"| Name |",
				"Secret nav",
				"from-script",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			input := copyFixture(t, dir, tc.file)
			output := filepath.Join(dir, "out.md")

			err := new(HTML).Convert(t.Context(), slog.New(slog.DiscardHandler), input, output, markdown.Options{
				Flavour:  tc.flavour,
				Filename: tc.file,
			})
			if err != nil {
				t.Fatalf("convert: %v", err)
			}

			got := readString(t, output)
			for _, snippet := range tc.want {
				if !strings.Contains(got, snippet) {
					t.Fatalf("markdown missing %q\n%s", snippet, got)
				}
			}

			for _, snippet := range tc.absent {
				if strings.Contains(got, snippet) {
					t.Fatalf("markdown contains %q\n%s", snippet, got)
				}
			}
		})
	}
}

func TestConvertHTML_Images(t *testing.T) {
	png := mustPNG(t)

	for _, tc := range []struct {
		name      string
		file      string
		filename  string
		extract   bool
		setup     func(t *testing.T, dir, input string)
		want      []string
		absent    []string
		wantImage string
		noImages  bool
	}{
		{
			name:      "data uri extracted",
			file:      "data-image.html",
			filename:  "notes.html",
			extract:   true,
			want:      []string{"![pixel](notes-1.png)"},
			absent:    []string{"data:image"},
			wantImage: "notes-1.png",
		},
		{
			name:     "data uri kept inline when extraction is off",
			file:     "data-image.html",
			filename: "notes.html",
			extract:  false,
			want:     []string{"data:image/png;base64,"},
			noImages: true,
		},
		{
			name:     "local file copied and prefixed with the stem",
			file:     "local-image.html",
			filename: "notes.html",
			extract:  true,
			setup: func(t *testing.T, dir, _ string) {
				t.Helper()
				err := os.WriteFile(filepath.Join(dir, "pixel.png"), png, 0o600)
				if err != nil {
					t.Fatalf("write pixel: %v", err)
				}
			},
			want:      []string{"![pixel](notes-1.png)"},
			wantImage: "notes-1.png",
		},
		{
			name:     "htm stem",
			file:     "local-image.html",
			filename: "Page.HTM",
			extract:  true,
			setup: func(t *testing.T, dir, _ string) {
				t.Helper()
				err := os.WriteFile(filepath.Join(dir, "pixel.png"), png, 0o600)
				if err != nil {
					t.Fatalf("write pixel: %v", err)
				}
			},
			want:      []string{"![pixel](Page-1.png)"},
			wantImage: "Page-1.png",
		},
		{
			name:     "remote url is not fetched",
			file:     "remote-image.html",
			filename: "notes.html",
			extract:  true,
			want:     []string{"https://example.com/pixel.png"},
			noImages: true,
		},
		{
			name:     "path outside the input directory is not read",
			file:     "escape.html",
			filename: "notes.html",
			extract:  true,
			setup: func(t *testing.T, dir, _ string) {
				t.Helper()
				err := os.WriteFile(filepath.Join(filepath.Dir(dir), "secret.png"), []byte("secret"), 0o600)
				if err != nil {
					t.Fatalf("write secret: %v", err)
				}
			},
			want:     []string{"../secret.png"},
			noImages: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			dir := filepath.Join(root, "in")
			err := os.Mkdir(dir, 0o755)
			if err != nil {
				t.Fatalf("mkdir: %v", err)
			}

			input := copyFixture(t, dir, tc.file)
			if tc.setup != nil {
				tc.setup(t, dir, input)
			}

			output := filepath.Join(dir, "out.md")
			imageDir := filepath.Join(dir, "images")
			err = os.Mkdir(imageDir, 0o755)
			if err != nil {
				t.Fatalf("mkdir images: %v", err)
			}

			options := markdown.Options{
				Flavour:       markdown.FlavourGFM,
				Filename:      tc.filename,
				ExtractImages: tc.extract,
			}
			if tc.extract {
				options.ImageDir = imageDir
			}

			err = new(HTML).Convert(t.Context(), slog.New(slog.DiscardHandler), input, output, options)
			if err != nil {
				t.Fatalf("convert: %v", err)
			}

			got := readString(t, output)
			for _, snippet := range tc.want {
				if !strings.Contains(got, snippet) {
					t.Fatalf("markdown missing %q\n%s", snippet, got)
				}
			}

			for _, snippet := range tc.absent {
				if strings.Contains(got, snippet) {
					t.Fatalf("markdown contains %q\n%s", snippet, got)
				}
			}

			entries, err := os.ReadDir(imageDir)
			if err != nil {
				t.Fatalf("read images: %v", err)
			}

			if tc.noImages && len(entries) != 0 {
				t.Fatalf("image dir = %d files, want none", len(entries))
			}

			if tc.wantImage == "" {
				return
			}

			if len(entries) != 1 || entries[0].Name() != tc.wantImage {
				names := make([]string, len(entries))
				for i, entry := range entries {
					names[i] = entry.Name()
				}

				t.Fatalf("images = %v, want [%s]", names, tc.wantImage)
			}

			written := readBytes(t, filepath.Join(imageDir, tc.wantImage))
			if !bytes.Equal(written, png) {
				t.Fatalf("image bytes = %d, want the fixture png (%d)", len(written), len(png))
			}
		})
	}
}

func TestConvertHTML_SymlinkOutsideInputDir(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(root, "secret.png")
	err := os.WriteFile(outside, []byte("secret"), 0o600)
	if err != nil {
		t.Fatalf("write secret: %v", err)
	}

	dir := filepath.Join(root, "in")
	err = os.Mkdir(dir, 0o755)
	if err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	err = os.Symlink(outside, filepath.Join(dir, "pixel.png"))
	if err != nil {
		t.Fatalf("symlink: %v", err)
	}

	input := copyFixture(t, dir, "local-image.html")
	output := filepath.Join(dir, "out.md")
	imageDir := filepath.Join(dir, "images")
	err = os.Mkdir(imageDir, 0o755)
	if err != nil {
		t.Fatalf("mkdir images: %v", err)
	}

	err = new(HTML).Convert(t.Context(), slog.New(slog.DiscardHandler), input, output, markdown.Options{
		Flavour:       markdown.FlavourGFM,
		Filename:      "notes.html",
		ExtractImages: true,
		ImageDir:      imageDir,
	})
	if err != nil {
		t.Fatalf("convert: %v", err)
	}

	entries, err := os.ReadDir(imageDir)
	if err != nil {
		t.Fatalf("read images: %v", err)
	}

	if len(entries) != 0 {
		t.Fatalf("image dir = %d files, want none", len(entries))
	}

	if !strings.Contains(readString(t, output), "pixel.png") {
		t.Fatal("markdown dropped the image link")
	}
}

func TestConvertHTML_Errors(t *testing.T) {
	dir := t.TempDir()
	input := copyFixture(t, dir, "article.html")
	output := filepath.Join(dir, "out.md")
	logger := slog.New(slog.DiscardHandler)

	err := new(HTML).Convert(nil, logger, input, output, markdown.Options{Filename: "article.html"})
	if err == nil {
		t.Fatal("expected an error for a nil context")
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	err = new(HTML).Convert(ctx, logger, input, output, markdown.Options{Filename: "article.html"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}

	err = new(HTML).Convert(t.Context(), logger, input, output, markdown.Options{Filename: "notes.docx"})
	if !errors.Is(err, markdown.ErrFormatNotSupported) {
		t.Fatalf("error = %v, want ErrFormatNotSupported", err)
	}

	err = new(HTML).Convert(t.Context(), logger, input, output, markdown.Options{
		Filename:      "article.html",
		ExtractImages: true,
	})
	if err == nil {
		t.Fatal("expected an error when ImageDir is empty")
	}

	err = ConvertHTMLBytes(t.Context(), logger, "", bytes.Repeat([]byte("a"), maxHTMLBytes+1), output, markdown.Options{})
	if err == nil {
		t.Fatal("expected an error for a document over the size limit")
	}
}

func TestConvertHTML_UpperCaseExtension(t *testing.T) {
	dir := t.TempDir()
	input := copyFixture(t, dir, "article.html")
	output := filepath.Join(dir, "out.md")

	err := new(HTML).Convert(t.Context(), slog.New(slog.DiscardHandler), input, output, markdown.Options{
		Filename: "Notes.HTML",
	})
	if err != nil {
		t.Fatalf("convert: %v", err)
	}

	if !strings.Contains(readString(t, output), "# HTML title") {
		t.Fatal("upper case extension was not converted")
	}
}

func TestConvertHTML_ZeroFlavourIsGFM(t *testing.T) {
	dir := t.TempDir()
	input := copyFixture(t, dir, "article.html")
	output := filepath.Join(dir, "out.md")

	err := new(HTML).Convert(t.Context(), slog.New(slog.DiscardHandler), input, output, markdown.Options{
		Filename: "article.html",
	})
	if err != nil {
		t.Fatalf("convert: %v", err)
	}

	if !strings.Contains(readString(t, output), "~~gone~~") {
		t.Fatal("zero flavour did not emit strikethrough")
	}
}

func copyFixture(t *testing.T, dir, name string) string {
	t.Helper()

	body, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	path := filepath.Join(dir, name)
	err = os.WriteFile(path, body, 0o600)
	if err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	return path
}

func readString(t *testing.T, path string) string {
	t.Helper()

	return string(readBytes(t, path))
}

func readBytes(t *testing.T, path string) []byte {
	t.Helper()

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}

	return body
}

func mustPNG(t *testing.T) []byte {
	t.Helper()

	raw, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==")
	if err != nil {
		t.Fatalf("decode png: %v", err)
	}

	return raw
}
