package markdown

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/labstack/echo/v5"

	"github.com/gotenberg/gotenberg/v8/pkg/gotenberg"
	"github.com/gotenberg/gotenberg/v8/pkg/modules/api"
)

func requestContext(t *testing.T, dir string, files map[string]string, values map[string][]string) *api.ContextMock {
	t.Helper()

	ctx := &api.Context{}
	mock := &api.ContextMock{Context: ctx}
	mock.SetDirPath(dir)
	mock.SetFiles(files)

	if values == nil {
		values = map[string][]string{}
	}

	mock.SetValues(values)
	mock.SetLogger(slog.New(slog.DiscardHandler))
	mock.SetMkdirAll(new(gotenberg.OsMkdirAll))
	ctx.Context = t.Context()

	return mock
}

func callRoute(t *testing.T, route api.Route, mock *api.ContextMock) error {
	t.Helper()

	c := echo.New().NewContext(
		httptest.NewRequest(http.MethodPost, ConvertRoutePath, nil),
		httptest.NewRecorder(),
	)
	c.Set("context", mock.Context)

	return route.Handler(c)
}

func plaintextRoute() api.Route {
	return convertRoute(NewRegistry([]MarkdownEngine{new(Plaintext)}), Options{Flavour: FlavourGFM})
}

func TestConvertRoute_TextAndMarkdown(t *testing.T) {
	for _, tc := range []struct {
		name     string
		filename string
		content  string
		wantName string
	}{
		{name: "txt", filename: "hello.txt", content: "Hello from plaintext.\n", wantName: "hello.md"},
		{name: "md", filename: "hello.md", content: "# Already markdown\n", wantName: "hello.md"},
		{name: "upper case", filename: "Hello.TXT", content: "Hello\n", wantName: "Hello.md"},
		{name: "markdown extension", filename: "notes.markdown", content: "notes\n", wantName: "notes.md"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			input := filepath.Join(dir, tc.filename)
			err := os.WriteFile(input, []byte(tc.content), 0o600)
			if err != nil {
				t.Fatalf("write input: %v", err)
			}

			mock := requestContext(t, dir, map[string]string{tc.filename: input}, nil)
			err = callRoute(t, plaintextRoute(), mock)
			if err != nil {
				t.Fatalf("route: %v", err)
			}

			paths := mock.OutputPaths()
			if len(paths) != 1 {
				t.Fatalf("outputs = %d, want 1", len(paths))
			}

			if got := mock.OriginalFilename(paths[0]); got != tc.wantName {
				t.Fatalf("filename = %q, want %q", got, tc.wantName)
			}

			body, err := os.ReadFile(paths[0])
			if err != nil {
				t.Fatalf("read output: %v", err)
			}

			if string(body) != tc.content {
				t.Fatalf("output = %q, want %q", body, tc.content)
			}

			built, err := mock.BuildOutputFile()
			if err != nil {
				t.Fatalf("build output: %v", err)
			}

			if built != paths[0] {
				t.Fatalf("single output was archived: %s", built)
			}
		})
	}
}

func TestConvertRoute_ManyFilesAreZipped(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{}

	for name, content := range map[string]string{
		"alpha.txt": "alpha\n",
		"beta.txt":  "beta\n",
	} {
		path := filepath.Join(dir, name)
		err := os.WriteFile(path, []byte(content), 0o600)
		if err != nil {
			t.Fatalf("write %s: %v", name, err)
		}

		files[name] = path
	}

	mock := requestContext(t, dir, files, nil)
	err := callRoute(t, plaintextRoute(), mock)
	if err != nil {
		t.Fatalf("route: %v", err)
	}

	got := zipEntries(t, mock.Context)
	want := map[string]string{
		"alpha.md": "alpha\n",
		"beta.md":  "beta\n",
	}

	if len(got) != len(want) {
		t.Fatalf("zip entries = %v, want %v", got, want)
	}

	for name, content := range want {
		if got[name] != content {
			t.Fatalf("zip %s = %q, want %q", name, got[name], content)
		}
	}
}

func TestConvertRoute_DuplicateStem(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{}

	for name, content := range map[string]string{
		"a.md":  "from-md",
		"a.txt": "from-txt",
	} {
		path := filepath.Join(dir, name)
		err := os.WriteFile(path, []byte(content), 0o600)
		if err != nil {
			t.Fatalf("write %s: %v", name, err)
		}

		files[name] = path
	}

	mock := requestContext(t, dir, files, nil)
	err := callRoute(t, plaintextRoute(), mock)
	if err != nil {
		t.Fatalf("route: %v", err)
	}

	got := zipEntries(t, mock.Context)
	if got["a.md"] != "from-md" || got["a (2).md"] != "from-txt" {
		t.Fatalf("zip entries = %v, want a.md=from-md and a (2).md=from-txt", got)
	}
}

func TestConvertRoute_ExtractImages(t *testing.T) {
	engine := &captureEngine{exts: []string{".txt"}}
	route := convertRoute(NewRegistry([]MarkdownEngine{engine}), Options{Flavour: FlavourGFM})

	dir := t.TempDir()
	input := filepath.Join(dir, "hello.txt")
	err := os.WriteFile(input, []byte("hello"), 0o600)
	if err != nil {
		t.Fatalf("write input: %v", err)
	}

	mock := requestContext(t, dir, map[string]string{"hello.txt": input}, map[string][]string{
		"flavour":       {"commonmark"},
		"extractImages": {"true"},
	})
	err = callRoute(t, route, mock)
	if err != nil {
		t.Fatalf("route: %v", err)
	}

	if engine.got.Flavour != FlavourCommonMark || !engine.got.ExtractImages || engine.got.ImageDir == "" || engine.got.Filename != "hello.txt" {
		t.Fatalf("options = %+v, want commonmark, images, a directory, and hello.txt", engine.got)
	}

	got := zipEntries(t, mock.Context)
	if got["hello.md"] != "markdown" || got["hello-1.png"] != "png" {
		t.Fatalf("zip entries = %v, want hello.md and hello-1.png", got)
	}
}

func TestConvertRoute_Errors(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "hello.txt")
	err := os.WriteFile(input, []byte("hello"), 0o600)
	if err != nil {
		t.Fatalf("write input: %v", err)
	}

	for _, tc := range []struct {
		name    string
		files   map[string]string
		values  map[string][]string
		want    int
		wantMsg string
	}{
		{
			name:    "no file",
			want:    http.StatusBadRequest,
			wantMsg: noInputFileMessage,
		},
		{
			name: "supported file does not excuse an unsupported one",
			files: map[string]string{
				"hello.txt": input,
				"page.docx": filepath.Join(dir, "page.docx"),
			},
			want:    http.StatusBadRequest,
			wantMsg: "Invalid 'files' form field: unsupported extension '.docx'. Valid extensions: .markdown, .md, .text, .txt.",
		},
		{
			name: "several unsupported extensions",
			files: map[string]string{
				"b.xyz":  filepath.Join(dir, "b.xyz"),
				"a.docx": filepath.Join(dir, "a.docx"),
			},
			want:    http.StatusBadRequest,
			wantMsg: "Invalid 'files' form field: unsupported extensions '.docx', '.xyz'. Valid extensions: .markdown, .md, .text, .txt.",
		},
		{
			name: "no extension",
			files: map[string]string{
				"README": filepath.Join(dir, "README"),
			},
			want:    http.StatusBadRequest,
			wantMsg: "Invalid 'files' form field: unsupported extension '(none)'. Valid extensions: .markdown, .md, .text, .txt.",
		},
		{
			name:    "invalid flavour",
			files:   map[string]string{"hello.txt": input},
			values:  map[string][]string{"flavour": {"latex"}},
			want:    http.StatusBadRequest,
			wantMsg: "Invalid form data: form field 'flavour' is invalid (got 'latex', resulting to value is not 'gfm' or 'commonmark')",
		},
		{
			name:    "invalid extractImages",
			files:   map[string]string{"hello.txt": input},
			values:  map[string][]string{"extractImages": {"maybe"}},
			want:    http.StatusBadRequest,
			wantMsg: "form field 'extractImages' is invalid",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := requestContext(t, dir, tc.files, tc.values)
			err := callRoute(t, plaintextRoute(), mock)
			if err == nil {
				t.Fatal("expected an error, got none")
			}

			status, message := api.ParseError(err)
			if status != tc.want {
				t.Fatalf("status = %d, want %d (message: %s)", status, tc.want, message)
			}

			if tc.name == "invalid extractImages" {
				if !bytes.Contains([]byte(message), []byte(tc.wantMsg)) {
					t.Fatalf("message = %q, want it to contain %q", message, tc.wantMsg)
				}

				return
			}

			if message != tc.wantMsg {
				t.Fatalf("message = %q, want %q", message, tc.wantMsg)
			}

			if len(mock.OutputPaths()) != 0 {
				t.Fatalf("outputs = %v, want none", mock.OutputPaths())
			}
		})
	}
}

type captureEngine struct {
	exts []string
	got  Options
}

func (e *captureEngine) Extensions() []string { return e.exts }

func (e *captureEngine) Convert(_ context.Context, _ *slog.Logger, _, outputPath string, options Options) error {
	e.got = options

	err := os.WriteFile(outputPath, []byte("markdown"), 0o600)
	if err != nil {
		return err
	}

	if options.ImageDir == "" {
		return nil
	}

	return os.WriteFile(filepath.Join(options.ImageDir, "hello-1.png"), []byte("png"), 0o600)
}

func zipEntries(t *testing.T, ctx *api.Context) map[string]string {
	t.Helper()

	out, err := ctx.BuildOutputFile()
	if err != nil {
		t.Fatalf("build output: %v", err)
	}

	reader, err := zip.OpenReader(out)
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}

	defer func() {
		err := reader.Close()
		if err != nil {
			t.Fatalf("close zip: %v", err)
		}
	}()

	got := make(map[string]string, len(reader.File))
	for _, file := range reader.File {
		rc, err := file.Open()
		if err != nil {
			t.Fatalf("open %s: %v", file.Name, err)
		}

		body, err := io.ReadAll(rc)
		closeErr := rc.Close()
		if err != nil {
			t.Fatalf("read %s: %v", file.Name, err)
		}

		if closeErr != nil {
			t.Fatalf("close %s: %v", file.Name, closeErr)
		}

		got[file.Name] = string(body)
	}

	return got
}
