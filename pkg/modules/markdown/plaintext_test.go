package markdown

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

func TestPlaintext_Convert(t *testing.T) {
	engine := new(Plaintext)

	for _, tc := range []struct {
		name     string
		filename string
		content  string
		wantErr  error
	}{
		{name: "txt", filename: "notes.txt", content: "hello\n"},
		{name: "text", filename: "notes.text", content: "hello"},
		{name: "md", filename: "notes.md", content: "# hi\n"},
		{name: "markdown", filename: "notes.markdown", content: "hi"},
		{name: "upper case extension", filename: "notes.TXT", content: "hello"},
		{name: "docx is not plaintext", filename: "notes.docx", content: "nope", wantErr: ErrFormatNotSupported},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			// The disk name has no extension on purpose. Dispatch uses
			// Options.Filename, which is what the route sets from the upload.
			input := filepath.Join(dir, "blob")
			output := filepath.Join(dir, "out.md")

			err := os.WriteFile(input, []byte(tc.content), 0o600)
			if err != nil {
				t.Fatalf("write input: %v", err)
			}

			err = engine.Convert(t.Context(), slog.New(slog.DiscardHandler), input, output, Options{Filename: tc.filename})
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("error = %v, want %v", err, tc.wantErr)
				}

				return
			}

			if err != nil {
				t.Fatalf("convert: %v", err)
			}

			got, err := os.ReadFile(output)
			if err != nil {
				t.Fatalf("read output: %v", err)
			}

			if string(got) != tc.content {
				t.Fatalf("output = %q, want %q", got, tc.content)
			}
		})
	}
}

func TestPlaintext_ConvertCancelled(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "blob")
	output := filepath.Join(dir, "out.md")

	err := os.WriteFile(input, []byte("hello"), 0o600)
	if err != nil {
		t.Fatalf("write input: %v", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	err = new(Plaintext).Convert(ctx, slog.New(slog.DiscardHandler), input, output, Options{Filename: "notes.txt"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}

func TestPlaintext_ConvertNilContext(t *testing.T) {
	err := new(Plaintext).Convert(nil, nil, "in.txt", "out.md", Options{Filename: "notes.txt"})
	if err == nil {
		t.Fatal("expected an error, got none")
	}
}

func TestPlaintext_Extensions(t *testing.T) {
	got := new(Plaintext).Extensions()
	want := []string{".txt", ".text", ".md", ".markdown"}

	if len(got) != len(want) {
		t.Fatalf("extensions = %v, want %v", got, want)
	}

	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("extensions = %v, want %v", got, want)
		}
	}
}
