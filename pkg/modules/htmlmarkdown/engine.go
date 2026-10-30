package htmlmarkdown

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/gotenberg/gotenberg/v8/pkg/gotenberg"
	"github.com/gotenberg/gotenberg/v8/pkg/modules/markdown"
)

func init() {
	gotenberg.MustRegisterModule(new(HTML))
}

// HTML converts .html and .htm files to Markdown.
//
// The bytes are converted in process. Flavour selects CommonMark or
// GitHub Flavored Markdown. ExtractImages writes data URIs and image files
// that sit next to the input. JavaScript in the file does not run.
type HTML struct{}

// Descriptor returns an [HTML]'s module descriptor.
func (e *HTML) Descriptor() gotenberg.ModuleDescriptor {
	return gotenberg.ModuleDescriptor{
		ID:  "html",
		New: func() gotenberg.Module { return new(HTML) },
	}
}

// Extensions returns the extensions [HTML] accepts.
func (e *HTML) Extensions() []string {
	return []string{".html", ".htm"}
}

// Convert reads inputPath and writes Markdown to outputPath.
func (e *HTML) Convert(ctx context.Context, logger *slog.Logger, inputPath, outputPath string, options markdown.Options) error {
	if ctx == nil {
		return errors.New("convert html to markdown: nil context")
	}

	if err := ctx.Err(); err != nil {
		return fmt.Errorf("convert html to markdown: %w", err)
	}

	ext := extensionOf(inputPath, options)
	if !e.supports(ext) {
		return fmt.Errorf("html engine: %w", markdown.ErrFormatNotSupported)
	}

	return ConvertHTML(ctx, logger, inputPath, outputPath, options)
}

// SystemMessages returns the extensions this engine claims.
func (e *HTML) SystemMessages() []string {
	return []string{"extensions - .html .htm"}
}

// Debug returns the extensions this engine claims.
func (e *HTML) Debug() map[string]any {
	return map[string]any{
		"extensions": e.Extensions(),
	}
}

func (e *HTML) supports(ext string) bool {
	return slices.Contains(e.Extensions(), ext)
}

func extensionOf(inputPath string, options markdown.Options) string {
	name := strings.TrimSpace(options.Filename)
	if name == "" {
		name = inputPath
	}

	ext := strings.ToLower(strings.TrimSpace(filepath.Ext(name)))
	if ext == "." {
		return ""
	}

	return ext
}

// ConvertHTML reads the HTML file at inputPath and writes Markdown to outputPath.
//
// Relative images are resolved against the directory that contains inputPath.
// The office engine can call this with the HTML LibreOffice wrote and the
// same [markdown.Options] the convert route built.
func ConvertHTML(ctx context.Context, logger *slog.Logger, inputPath, outputPath string, options markdown.Options) error {
	// The path is a file this request stored, or a file the caller wrote.
	body, err := os.ReadFile(inputPath) //nolint:gosec
	if err != nil {
		return fmt.Errorf("open html file: %w", err)
	}

	return ConvertHTMLBytes(ctx, logger, inputPath, body, outputPath, options)
}

// Interface guards.
var (
	_ gotenberg.Module        = (*HTML)(nil)
	_ gotenberg.SystemLogger  = (*HTML)(nil)
	_ gotenberg.Debuggable    = (*HTML)(nil)
	_ markdown.MarkdownEngine = (*HTML)(nil)
)
