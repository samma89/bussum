package markdown

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"slices"

	"github.com/gotenberg/gotenberg/v8/pkg/gotenberg"
)

func init() {
	gotenberg.MustRegisterModule(new(Plaintext))
}

// Plaintext copies .txt, .text, .md, and .markdown files to Markdown.
//
// The bytes are unchanged. A .txt file is Markdown that happens to contain
// no markup, and a .md file is already Markdown. Flavour and ExtractImages
// are accepted and ignored: there is no structure to rewrite and no image
// to extract. This keeps POST /forms/markdown/convert working before the
// office, HTML, and PDF engines exist.
type Plaintext struct{}

// Descriptor returns a [Plaintext]'s module descriptor.
func (e *Plaintext) Descriptor() gotenberg.ModuleDescriptor {
	return gotenberg.ModuleDescriptor{
		ID:  "plaintext",
		New: func() gotenberg.Module { return new(Plaintext) },
	}
}

// Extensions returns the extensions [Plaintext] accepts.
func (e *Plaintext) Extensions() []string {
	return []string{".txt", ".text", ".md", ".markdown"}
}

// Convert copies inputPath to outputPath.
func (e *Plaintext) Convert(ctx context.Context, logger *slog.Logger, inputPath, outputPath string, options Options) error {
	if ctx == nil {
		return errors.New("convert plaintext to markdown: nil context")
	}

	if err := ctx.Err(); err != nil {
		return fmt.Errorf("convert plaintext to markdown: %w", err)
	}

	ext := inputExt(inputPath, options)
	if !e.supports(ext) {
		return fmt.Errorf("plaintext engine: %w", ErrFormatNotSupported)
	}

	// The path is a file the API stored in this request's directory.
	in, err := os.Open(inputPath) //nolint:gosec
	if err != nil {
		return fmt.Errorf("open plaintext file: %w", err)
	}

	defer func() {
		closeErr := in.Close()
		if closeErr != nil && logger != nil {
			logger.ErrorContext(ctx, fmt.Sprintf("close plaintext file: %s", closeErr))
		}
	}()

	out, err := os.Create(outputPath) //nolint:gosec
	if err != nil {
		return fmt.Errorf("create markdown file: %w", err)
	}

	n, err := io.Copy(out, &cancelReader{ctx: ctx, r: in})
	if err != nil {
		_ = out.Close()
		return fmt.Errorf("copy plaintext to markdown: %w", err)
	}

	err = out.Close()
	if err != nil {
		return fmt.Errorf("close markdown file: %w", err)
	}

	if logger != nil {
		logger.DebugContext(ctx, fmt.Sprintf("plaintext copied %d bytes to markdown", n))
	}

	return nil
}

func (e *Plaintext) supports(ext string) bool {
	return slices.Contains(e.Extensions(), ext)
}

// cancelReader stops a copy when the request context is cancelled.
type cancelReader struct {
	ctx context.Context
	r   io.Reader
}

func (c *cancelReader) Read(p []byte) (int, error) {
	err := c.ctx.Err()
	if err != nil {
		return 0, err
	}

	return c.r.Read(p)
}

// Interface guards.
var (
	_ gotenberg.Module = (*Plaintext)(nil)
	_ MarkdownEngine   = (*Plaintext)(nil)
)
