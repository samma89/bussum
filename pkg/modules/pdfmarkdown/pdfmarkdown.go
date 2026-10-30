package pdfmarkdown

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"unicode"

	flag "github.com/spf13/pflag"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	semconv "go.opentelemetry.io/otel/semconv/v1.40.0"
	"go.opentelemetry.io/otel/trace"

	"github.com/gotenberg/gotenberg/v8/pkg/gotenberg"
	"github.com/gotenberg/gotenberg/v8/pkg/modules/markdown"
)

func init() {
	gotenberg.MustRegisterModule(new(Pdf))
}

const (
	// defaultPageSeparator is the Markdown written between pages when the
	// operator has not set --pdf-markdown-page-separator.
	defaultPageSeparator = "---"

	pdftohtmlBinEnv = "PDFTOHTML_BIN_PATH"
)

// ErrEncrypted is returned when the PDF has an encryption dictionary.
// Password-protected files and owner-password files both carry one. Poppler
// can sometimes still read the latter. This engine rejects both, because an
// encrypted file must not come back as an empty document.
var ErrEncrypted = errors.New("pdf is encrypted")

// ErrNoText is returned when the PDF has no extractable text. A scanned
// page is an image with no text layer. OCR is out of scope.
var ErrNoText = errors.New("pdf has no extractable text")

// encryptDict matches a PDF encryption dictionary reference or an inline
// encryption dictionary. The trailer keeps this token in the clear.
var encryptDict = regexp.MustCompile(`/Encrypt\s*(?:<<|\d+\s+\d+\s+R)`)

// Pdf converts .pdf files to Markdown.
//
// The module ID is "pdf", which is the name --markdown-engines selects.
type Pdf struct {
	binPath       string
	pageSeparator string

	version     string
	versionOnce sync.Once
}

// Descriptor returns a [Pdf]'s module descriptor.
func (engine *Pdf) Descriptor() gotenberg.ModuleDescriptor {
	return gotenberg.ModuleDescriptor{
		ID: "pdf",
		FlagSet: func() *flag.FlagSet {
			fs := flag.NewFlagSet("pdf", flag.ExitOnError)
			fs.String(
				"pdf-markdown-page-separator",
				defaultPageSeparator,
				"Markdown written between PDF pages. Empty inserts a blank line only. \\n in the value becomes a newline",
			)

			return fs
		}(),
		New: func() gotenberg.Module { return new(Pdf) },
	}
}

// Provision sets the pdftohtml path and the page separator.
func (engine *Pdf) Provision(ctx *gotenberg.Context) error {
	binPath := strings.TrimSpace(os.Getenv(pdftohtmlBinEnv))
	if binPath == "" {
		return errors.New("PDFTOHTML_BIN_PATH environment variable is not set; set it to the absolute path of the pdftohtml binary")
	}

	engine.binPath = binPath
	flags := ctx.ParsedFlags()
	engine.pageSeparator = unescapeSeparator(flags.MustString("pdf-markdown-page-separator"))

	return nil
}

// Validate checks that the pdftohtml binary exists.
func (engine *Pdf) Validate() error {
	_, err := os.Stat(engine.binPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("pdftohtml binary %q does not exist; set %s to the absolute path of the pdftohtml binary", engine.binPath, pdftohtmlBinEnv)
		}

		return fmt.Errorf("stat pdftohtml binary %q: %w; set %s to the absolute path of the pdftohtml binary", engine.binPath, err, pdftohtmlBinEnv)
	}

	return nil
}

// Debug returns the pdftohtml version.
func (engine *Pdf) Debug() map[string]any {
	return map[string]any{
		"version":        engine.detectVersion(),
		"page_separator": engine.pageSeparator,
	}
}

// Extensions returns the extensions [Pdf] accepts.
func (engine *Pdf) Extensions() []string {
	return []string{".pdf"}
}

// Convert reads a PDF and writes Markdown.
func (engine *Pdf) Convert(ctx context.Context, logger *slog.Logger, inputPath, outputPath string, options markdown.Options) error {
	if ctx == nil {
		return errors.New("convert pdf to markdown: nil context")
	}

	if err := ctx.Err(); err != nil {
		return fmt.Errorf("convert pdf to markdown: %w", err)
	}

	logger = nonNilLogger(logger)
	name := displayName(inputPath, options)

	ext := strings.ToLower(filepath.Ext(name))
	if ext != ".pdf" {
		return fmt.Errorf("pdf engine: %w", markdown.ErrFormatNotSupported)
	}

	if options.ExtractImages && strings.TrimSpace(options.ImageDir) == "" {
		return errors.New("extract images: image directory is empty")
	}

	encrypted, err := pdfEncrypted(ctx, inputPath)
	if err != nil {
		return fmt.Errorf("inspect pdf %q: %w", name, err)
	}

	if encrypted {
		logger.WarnContext(ctx, fmt.Sprintf("reject encrypted pdf %q", name))

		return encryptedInput(name)
	}

	ctx, span := gotenberg.Tracer().Start(ctx, "pdf.Convert",
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(engine.spanAttrs()...),
	)
	defer span.End()

	fail := func(err error) error {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())

		return err
	}

	dir, err := os.MkdirTemp("", "gotenberg-pdfmarkdown-*")
	if err != nil {
		return fail(fmt.Errorf("create pdftohtml directory: %w", err))
	}

	defer func() {
		removeErr := os.RemoveAll(dir)
		if removeErr != nil {
			logger.ErrorContext(ctx, fmt.Sprintf("remove pdftohtml directory: %s", removeErr))
		}
	}()

	xmlPath := filepath.Join(dir, "out.xml")

	err = engine.runPdftohtml(ctx, logger, inputPath, xmlPath, options.ExtractImages)
	if err != nil {
		return fail(err)
	}

	// The path is a file this function just created.
	data, err := os.ReadFile(xmlPath) //nolint:gosec
	if err != nil {
		return fail(fmt.Errorf("read pdftohtml xml: %w", err))
	}

	rendered, err := renderXML(data, dir, options, engine.pageSeparator)
	if err != nil {
		return fail(fmt.Errorf("build markdown from pdftohtml xml: %w", err))
	}

	if !rendered.hasText {
		logger.WarnContext(ctx, fmt.Sprintf("reject pdf %q with no extractable text", name))

		return fail(noTextInput(name))
	}

	err = copyImages(rendered.images)
	if err != nil {
		return fail(err)
	}

	err = os.WriteFile(outputPath, []byte(rendered.markdown), 0o600)
	if err != nil {
		removeCopied(rendered.images)

		return fail(fmt.Errorf("write markdown file: %w", err))
	}

	span.SetStatus(codes.Ok, "")
	logger.DebugContext(ctx, fmt.Sprintf("converted pdf %q to %d bytes of markdown", name, len(rendered.markdown)))

	return nil
}

// runPdftohtml writes Poppler XML for inputPath.
//
// Poppler 25.03, which Debian 13 ships, has no pdftotext -markdown flag.
// pdftohtml -xml reports font size and position. The layout pass uses those
// for headings, columns, lists, and tables. -i drops images unless the
// caller asked for them. A cancelled context kills the process group, which
// is the same contract as the PDF engines.
func (engine *Pdf) runPdftohtml(ctx context.Context, logger *slog.Logger, inputPath, xmlPath string, extractImages bool) error {
	args := []string{
		"-xml",
		"-zoom", "1",
		"-enc", "UTF-8",
		"-fontfullname",
		"-noroundcoord",
		"-q",
	}
	if !extractImages {
		args = append(args, "-i")
	}

	args = append(args, inputPath, xmlPath)

	cmd, err := gotenberg.CommandContext(ctx, logger, engine.binPath, args...)
	if err != nil {
		return fmt.Errorf("create pdftohtml command: %w", err)
	}

	code, err := cmd.Exec()
	if err != nil {
		return fmt.Errorf("run pdftohtml: exit %d: %w", code, err)
	}

	return nil
}

func (engine *Pdf) spanAttrs(extra ...attribute.KeyValue) []attribute.KeyValue {
	attrs := make([]attribute.KeyValue, 0, 2+len(extra))
	attrs = append(attrs, semconv.ServerAddress(engine.binPath))
	if v := engine.detectVersion(); v != "" {
		attrs = append(attrs, attribute.String("gotenberg.pdf.version", v))
	}

	return append(attrs, extra...)
}

// detectVersion resolves the pdftohtml version once, preferring the value
// captured at image build time so a request does not spawn pdftohtml just
// to report a version.
func (engine *Pdf) detectVersion() string {
	engine.versionOnce.Do(func() {
		if v, ok := gotenberg.BuildVersion("pdf"); ok {
			engine.version = v

			return
		}

		cmd := exec.Command(engine.binPath, "-v") //nolint:gosec // binPath is validated at Provision.
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

		output, err := cmd.Output()
		if err != nil {
			engine.version = err.Error()

			return
		}

		line, _, _ := strings.Cut(string(output), "\n")
		engine.version = strings.TrimSpace(line)
		if engine.version == "" {
			engine.version = "Unable to determine pdftohtml version"
		}
	})

	return engine.version
}

// pdfEncrypted reports whether the file names an encryption dictionary.
//
// Poppler exits 1 both for a bad password and for a corrupt file, and
// gotenberg.Cmd does not return that stderr. The trailer keeps /Encrypt in
// the clear, so a protected file is rejected before pdftohtml can write an
// empty document.
func pdfEncrypted(ctx context.Context, path string) (encrypted bool, err error) {
	// The path is a file the API stored for this request.
	file, err := os.Open(path) //nolint:gosec
	if err != nil {
		return false, fmt.Errorf("open pdf: %w", err)
	}

	defer func() {
		closeErr := file.Close()
		if closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close pdf: %w", closeErr))
		}
	}()

	buf := make([]byte, 64*1024)
	var tail []byte

	for {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return false, ctxErr
		}

		n, readErr := file.Read(buf)
		chunk := make([]byte, 0, len(tail)+n)
		chunk = append(chunk, tail...)
		chunk = append(chunk, buf[:n]...)

		if encryptDict.Match(chunk) {
			return true, nil
		}

		if n > 64 {
			tail = append([]byte(nil), buf[n-64:n]...)
		} else {
			tail = append([]byte(nil), buf[:n]...)
		}

		if readErr == nil {
			continue
		}

		if errors.Is(readErr, io.EOF) {
			return false, nil
		}

		return false, fmt.Errorf("read pdf: %w", readErr)
	}
}

// inputError is a client-facing rejection. ParseError in the API package
// reads HttpError off the wrapped chain, so the convert route can keep
// returning the engine error unchanged.
type inputError struct {
	status  int
	message string
	err     error
}

func (e *inputError) Error() string {
	if e.err != nil {
		return e.err.Error()
	}

	return e.message
}

func (e *inputError) Unwrap() error { return e.err }

func (e *inputError) HttpError() (int, string) { return e.status, e.message }

func encryptedInput(name string) error {
	return &inputError{
		status:  http.StatusBadRequest,
		message: fmt.Sprintf("Invalid 'files' form field: '%s' is encrypted. Remove the password and try again.", name),
		err:     fmt.Errorf("pdf %q: %w", name, ErrEncrypted),
	}
}

func noTextInput(name string) error {
	return &inputError{
		status:  http.StatusBadRequest,
		message: fmt.Sprintf("Invalid 'files' form field: '%s' has no extractable text. Scanned pages need OCR, which this engine does not do.", name),
		err:     fmt.Errorf("pdf %q: %w", name, ErrNoText),
	}
}

func displayName(inputPath string, options markdown.Options) string {
	name := strings.TrimSpace(options.Filename)
	if name == "" {
		name = filepath.Base(inputPath)
	}

	return name
}

func unescapeSeparator(value string) string {
	return strings.ReplaceAll(value, `\n`, "\n")
}

func nonNilLogger(logger *slog.Logger) *slog.Logger {
	if logger != nil {
		return logger
	}

	return slog.New(slog.DiscardHandler)
}

func copyImages(images []imageJob) error {
	for i := range images {
		err := copyFile(images[i].src, images[i].dest)
		if err != nil {
			removeCopied(images[:i])

			return err
		}
	}

	return nil
}

func removeCopied(images []imageJob) {
	for _, image := range images {
		_ = os.Remove(image.dest)
	}
}

func copyFile(src, dest string) error {
	in, err := os.Open(src) //nolint:gosec // src is a file pdftohtml wrote in this request's temp directory.
	if err != nil {
		return fmt.Errorf("open extracted image: %w", err)
	}

	defer func() {
		_ = in.Close()
	}()

	out, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600) //nolint:gosec // dest is the image directory the route created.
	if err != nil {
		return fmt.Errorf("create extracted image: %w", err)
	}

	_, err = io.Copy(out, in)
	closeErr := out.Close()
	if err != nil {
		_ = os.Remove(dest)

		return fmt.Errorf("copy extracted image: %w", err)
	}

	if closeErr != nil {
		_ = os.Remove(dest)

		return fmt.Errorf("close extracted image: %w", closeErr)
	}

	return nil
}

func countText(s string) int {
	n := 0
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			n++
		}
	}

	return n
}

// Interface guards.
var (
	_ gotenberg.Module        = (*Pdf)(nil)
	_ gotenberg.Provisioner   = (*Pdf)(nil)
	_ gotenberg.Validator     = (*Pdf)(nil)
	_ gotenberg.Debuggable    = (*Pdf)(nil)
	_ markdown.MarkdownEngine = (*Pdf)(nil)
)
