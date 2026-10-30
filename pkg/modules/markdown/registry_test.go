package markdown

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

type recordingEngine struct {
	id   string
	exts []string
}

func (e *recordingEngine) Extensions() []string { return e.exts }

func (e *recordingEngine) Convert(_ context.Context, _ *slog.Logger, _, outputPath string, _ Options) error {
	return os.WriteFile(outputPath, []byte(e.id), 0o600)
}

func TestRegistry_Dispatch(t *testing.T) {
	first := &recordingEngine{id: "first", exts: []string{".txt", "md"}}
	second := &recordingEngine{id: "second", exts: []string{".txt", ".docx"}}
	registry := NewRegistry([]MarkdownEngine{nil, first, second})

	gotExts := registry.Extensions()
	wantExts := []string{".docx", ".md", ".txt"}
	if !reflect.DeepEqual(gotExts, wantExts) {
		t.Fatalf("extensions = %v, want %v", gotExts, wantExts)
	}

	engine, ok := registry.Lookup("TXT")
	if !ok || engine != first {
		t.Fatalf("lookup TXT = %v, %v, want the first engine", engine, ok)
	}

	engine, ok = registry.Lookup(".docx")
	if !ok || engine != second {
		t.Fatalf("lookup .docx = %v, %v, want the second engine", engine, ok)
	}

	if _, ok = registry.Lookup(".pdf"); ok {
		t.Fatal("lookup .pdf succeeded, want no engine")
	}

	dir := t.TempDir()
	output := filepath.Join(dir, "out.md")
	err := registry.Convert(t.Context(), slog.New(slog.DiscardHandler), filepath.Join(dir, "blob"), output, Options{Filename: "notes.docx"})
	if err != nil {
		t.Fatalf("convert: %v", err)
	}

	body, err := os.ReadFile(output)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}

	if string(body) != "second" {
		t.Fatalf("engine = %q, want %q", body, "second")
	}

	err = registry.Convert(t.Context(), nil, filepath.Join(dir, "blob"), output, Options{Filename: "notes.pdf"})
	if !errors.Is(err, ErrFormatNotSupported) {
		t.Fatalf("error = %v, want %v", err, ErrFormatNotSupported)
	}
}

func TestRegistry_Nil(t *testing.T) {
	var registry *Registry

	if exts := registry.Extensions(); exts != nil {
		t.Fatalf("extensions = %v, want nil", exts)
	}

	if _, ok := registry.Lookup(".txt"); ok {
		t.Fatal("lookup on a nil registry succeeded")
	}

	err := registry.Convert(t.Context(), nil, "in.txt", "out.md", Options{})
	if !errors.Is(err, ErrFormatNotSupported) {
		t.Fatalf("error = %v, want %v", err, ErrFormatNotSupported)
	}
}

func TestParseFlavour(t *testing.T) {
	for _, tc := range []struct {
		name    string
		value   string
		want    Flavour
		wantErr bool
	}{
		{name: "gfm", value: "gfm", want: FlavourGFM},
		{name: "gfm with space", value: " GFM ", want: FlavourGFM},
		{name: "commonmark", value: "commonmark", want: FlavourCommonMark},
		{name: "commonmark mixed case", value: "CommonMark", want: FlavourCommonMark},
		{name: "unknown", value: "latex", wantErr: true},
		{name: "empty", value: "", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseFlavour(tc.value)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected an error, got none")
				}

				return
			}

			if err != nil {
				t.Fatalf("parse: %v", err)
			}

			if got != tc.want {
				t.Fatalf("flavour = %q, want %q", got, tc.want)
			}
		})
	}
}
