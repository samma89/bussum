package markdown

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
)

const (
	// FlavourGFM is GitHub Flavored Markdown. It is the default because
	// tables, strikethrough, and autolinks are what office and HTML
	// conversions need to keep.
	FlavourGFM Flavour = "gfm"

	// FlavourCommonMark is CommonMark. It is the strict subset of [FlavourGFM].
	FlavourCommonMark Flavour = "commonmark"

	// DefaultFlavour is the flavour used when a request omits the form field
	// and the operator has not set --markdown-flavour (env MARKDOWN_FLAVOUR).
	DefaultFlavour = FlavourGFM

	// ConvertRoutePath is the multipart route that converts uploaded files
	// to Markdown.
	ConvertRoutePath = "/forms/markdown/convert"
)

// Flavour selects the Markdown syntax an engine should emit.
type Flavour string

// ErrFormatNotSupported is returned when no engine claims the input
// extension. The convert route maps it to HTTP 400.
var ErrFormatNotSupported = errors.New("markdown format not supported")

// Options configures one conversion.
//
// A zero [Flavour] means the engine decides. The convert route always sets
// Flavour. ExtractImages defaults to false: the response stays a single
// Markdown file, and engines must not write companion files. Set
// ExtractImages to ask for images. The route then creates ImageDir and
// archives every file the engine writes there next to the Markdown.
type Options struct {
	// Flavour is [FlavourGFM] or [FlavourCommonMark].
	Flavour Flavour

	// ExtractImages reports whether embedded images should be written.
	ExtractImages bool

	// ImageDir is the directory reserved for this input's images. The route
	// sets it when ExtractImages is true and leaves it empty otherwise. An
	// engine writes image files directly in this directory (not in a
	// subdirectory) and links them from the Markdown by base name, for
	// example ![alt](notes-1.png). Prefix names with the document stem so
	// two inputs in one request do not collide. The archive is flat.
	ImageDir string

	// Filename is the original upload name, including its extension. The
	// registry uses it to choose an engine. When it is empty, the registry
	// uses the extension of the input path.
	Filename string
}

// MarkdownEngine converts one file to Markdown.
//
// The concrete type also implements gotenberg.Module. Register it from
// init with gotenberg.MustRegisterModule and blank-import the package from
// pkg/standard. The markdown package must not import the engine, or the
// two packages cycle.
type MarkdownEngine interface {
	// Extensions returns the extensions this engine accepts, lower case,
	// with the leading dot. For example ".docx".
	Extensions() []string

	// Convert reads inputPath and writes Markdown to outputPath.
	//
	// inputPath is the file stored for this request. outputPath is where
	// the Markdown file must be created. options.Filename carries the
	// caller's name when the path itself is a UUID. A nil context is an
	// error. A cancelled context stops the conversion.
	Convert(ctx context.Context, logger *slog.Logger, inputPath, outputPath string, options Options) error
}

// Provider is the module interface for the aggregated engine.
//
//	mod, err := ctx.Module(new(markdown.Provider))
//	engine, err := mod.(markdown.Provider).Markdown()
type Provider interface {
	// Markdown returns the engine that dispatches by file extension.
	Markdown() (MarkdownEngine, error)
}

// ParseFlavour returns the canonical flavour for value.
//
// Matching is case-insensitive. Surrounding space is ignored. The error is
// safe to surface on a form field: it names the accepted values.
func ParseFlavour(value string) (Flavour, error) {
	flavour := Flavour(strings.ToLower(strings.TrimSpace(value)))
	switch flavour {
	case FlavourGFM, FlavourCommonMark:
		return flavour, nil
	default:
		return "", errors.New("value is not 'gfm' or 'commonmark'")
	}
}

// inputExt selects the extension the registry matches on.
func inputExt(inputPath string, options Options) string {
	name := strings.TrimSpace(options.Filename)
	if name == "" {
		name = inputPath
	}

	return normalizeExt(filepath.Ext(name))
}

// normalizeExt returns a lower-case extension with a leading dot.
// An empty extension stays empty.
func normalizeExt(ext string) string {
	ext = strings.ToLower(strings.TrimSpace(ext))
	if ext == "" || ext == "." {
		return ""
	}

	if !strings.HasPrefix(ext, ".") {
		ext = "." + ext
	}

	return ext
}
