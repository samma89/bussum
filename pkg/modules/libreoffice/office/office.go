package office

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"go.opentelemetry.io/otel/codes"
	semconv "go.opentelemetry.io/otel/semconv/v1.40.0"
	"go.opentelemetry.io/otel/trace"

	"github.com/gotenberg/gotenberg/v8/pkg/gotenberg"
	"github.com/gotenberg/gotenberg/v8/pkg/modules/libreoffice/api"
	"github.com/gotenberg/gotenberg/v8/pkg/modules/markdown"
)

func init() {
	gotenberg.MustRegisterModule(new(Office))
}

// htmlExporter is the LibreOffice HTML export used by [Office].
// [api.Uno] implements it. Tests substitute a fake.
type htmlExporter interface {
	Html(ctx context.Context, logger *slog.Logger, inputPath, outputPath string) error
}

// Office converts Office documents to Markdown.
//
// The LibreOffice process exports HTML. A Go library turns that HTML into
// Markdown. There is no flag on this module: the process, the queue, and the
// start timeout are the LibreOffice API's. The request context is the
// conversion deadline.
type Office struct {
	exporter htmlExporter
}

// Descriptor returns an [Office]'s module descriptor.
func (e *Office) Descriptor() gotenberg.ModuleDescriptor {
	return gotenberg.ModuleDescriptor{
		ID:  "office",
		New: func() gotenberg.Module { return new(Office) },
	}
}

// Provision wires the LibreOffice UNO API. HTML export goes through the same
// supervised process as PDF conversion.
func (e *Office) Provision(ctx *gotenberg.Context) error {
	provider, err := ctx.Module(new(api.Provider))
	if err != nil {
		return fmt.Errorf("get LibreOffice Uno provider: %w", err)
	}

	uno, err := provider.(api.Provider).LibreOffice()
	if err != nil {
		return fmt.Errorf("get LibreOffice Uno: %w", err)
	}

	e.exporter = uno

	return nil
}

// Extensions returns the extensions [Office] accepts.
func (e *Office) Extensions() []string {
	return []string{
		".docx", ".doc",
		".xlsx", ".xls",
		".pptx", ".ppt",
		".odt", ".ods", ".odp",
		".rtf",
	}
}

// Convert reads an Office document at inputPath and writes Markdown to outputPath.
//
// A zero [markdown.Options.Flavour] is GitHub Flavored Markdown. A cancelled
// context stops the conversion before LibreOffice is called, and the export
// itself uses that context so the process is killed with the request.
func (e *Office) Convert(ctx context.Context, logger *slog.Logger, inputPath, outputPath string, options markdown.Options) error {
	if ctx == nil {
		return errors.New("convert office document to markdown: nil context")
	}

	if err := ctx.Err(); err != nil {
		return fmt.Errorf("convert office document to markdown: %w", err)
	}

	if e.exporter == nil {
		return errors.New("convert office document to markdown: engine is not provisioned")
	}

	ext := extension(inputPath, options)
	if !e.supports(ext) {
		return fmt.Errorf("office engine: %w", markdown.ErrFormatNotSupported)
	}

	options, err := normalizeOptions(options)
	if err != nil {
		return fmt.Errorf("convert office document to markdown: %w", err)
	}

	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}

	ctx, span := gotenberg.Tracer().Start(ctx, "office.Convert",
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(semconv.ServerAddress("libreoffice")),
	)
	defer span.End()

	err = e.convert(ctx, logger, inputPath, outputPath, ext, options)
	if err != nil {
		gotenberg.SpanErrorType(span, gotenberg.ClassifyError(err))
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())

		return err
	}

	span.SetStatus(codes.Ok, "")

	return nil
}

func (e *Office) convert(ctx context.Context, logger *slog.Logger, inputPath, outputPath, ext string, options markdown.Options) error {
	dir, err := os.MkdirTemp("", "gotenberg-office-*")
	if err != nil {
		return fmt.Errorf("create office html directory: %w", err)
	}

	defer func() {
		removeErr := os.RemoveAll(dir)
		if removeErr != nil {
			logger.ErrorContext(ctx, fmt.Sprintf("remove office html export: %s", removeErr))
		}
	}()

	htmlPath := filepath.Join(dir, "document.html")

	err = e.exporter.Html(ctx, logger, inputPath, htmlPath)
	if err != nil {
		return fmt.Errorf("export office document to html: %w", err)
	}

	if err = ctx.Err(); err != nil {
		return fmt.Errorf("export office document to html: %w", err)
	}

	var names []string
	if documentKind(ext) == kindSheet {
		names, err = sheetNames(inputPath, ext)
		if err != nil {
			// The HTML export still has the cells. Sheet titles fall back to
			// "Sheet N" when the package cannot be read.
			logger.DebugContext(ctx, fmt.Sprintf("read sheet names: %s", err))
			names = nil
		}
	}

	markdownBytes, err := markdownFromExport(ctx, dir, htmlPath, documentKind(ext), names, options)
	if err != nil {
		return err
	}

	err = os.WriteFile(outputPath, markdownBytes, 0o600) //nolint:gosec // output path is the request directory
	if err != nil {
		return fmt.Errorf("write markdown file: %w", err)
	}

	logger.DebugContext(ctx, fmt.Sprintf("office converted %s to markdown (flavour %s, extract images %t)", ext, options.Flavour, options.ExtractImages))

	return nil
}

func (e *Office) supports(ext string) bool {
	return slices.Contains(e.Extensions(), ext)
}

func extension(inputPath string, options markdown.Options) string {
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

func normalizeOptions(options markdown.Options) (markdown.Options, error) {
	if options.Flavour == "" {
		options.Flavour = markdown.FlavourGFM
	}

	flavour, err := markdown.ParseFlavour(string(options.Flavour))
	if err != nil {
		return options, fmt.Errorf("markdown flavour: %w", err)
	}

	options.Flavour = flavour

	if options.ExtractImages && strings.TrimSpace(options.ImageDir) == "" {
		return options, errors.New("extract images: image directory is empty")
	}

	if options.ExtractImages {
		info, statErr := os.Stat(options.ImageDir)
		if statErr != nil {
			return options, fmt.Errorf("image directory: %w", statErr)
		}

		if !info.IsDir() {
			return options, fmt.Errorf("image directory %s is not a directory", options.ImageDir)
		}
	}

	return options, nil
}

// Interface guards.
var (
	_ gotenberg.Module        = (*Office)(nil)
	_ gotenberg.Provisioner   = (*Office)(nil)
	_ markdown.MarkdownEngine = (*Office)(nil)
	_ htmlExporter            = api.Uno(nil)
)
