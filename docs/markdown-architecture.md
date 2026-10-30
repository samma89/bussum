# Markdown conversion

The markdown module turns uploaded documents into Markdown and leaves the PDF modules in place. Office, HTML, and PDF engines plug into the same registry. This unit ships the registry, the route, and a plaintext engine.

## Route

`POST /forms/markdown/convert`

Multipart, same stack as the other `/forms` routes. The field `files` is repeatable. Optional fields:

| Field           | Values              | Default                                                             |
| --------------- | ------------------- | ------------------------------------------------------------------- |
| `flavour`       | `gfm`, `commonmark` | `gfm`, or `--markdown-flavour` (`MARKDOWN_FLAVOUR`)                 |
| `extractImages` | `true`, `false`     | `false`, or `--markdown-extract-images` (`MARKDOWN_EXTRACT_IMAGES`) |

One output is a Markdown file. More than one output is a zip. That is the API's existing behavior for every multi-file response: several inputs, or one Markdown file plus extracted images. `Gotenberg-Output-Filename` renames that file or the zip. `Gotenberg-Trace` is echoed. Webhook headers work because the route is multipart. Engine code does not reimplement them.

Empty `files` is HTTP 400: `Invalid 'files' form field: at least one file is required.`

An extension no engine claims is HTTP 400. The body names the extension and the extensions that are currently registered. A request that mixes a supported file with an unsupported one is rejected before any file is converted.

An engine failure is HTTP 500. The client sees `Internal Server Error`. The log has the wrapped error.

## Flags

| Flag                        | Environment               | Default     |
| --------------------------- | ------------------------- | ----------- |
| `--markdown-engines`        | `MARKDOWN_ENGINES`        | empty (all) |
| `--markdown-flavour`        | `MARKDOWN_FLAVOUR`        | `gfm`       |
| `--markdown-extract-images` | `MARKDOWN_EXTRACT_IMAGES` | `false`     |
| `--markdown-disable-routes` | `MARKDOWN_DISABLE_ROUTES` | `false`     |

`--markdown-engines` is a comma-separated list of module IDs. Empty means every registered engine, in module ID order. The first engine that claims an extension wins. There is no fallback: if that engine returns an error, the request fails. A second module may claim the same extension. The operator picks the winner by listing it first. A name that is not loaded fails startup and the message lists the engines that are loaded.

`gfm` is the default flavour. Office and HTML conversions need tables, strikethrough, and autolinks. `commonmark` is the strict subset, available per request and as the process default.

`extractImages` defaults to false. The usual response is one `.md` file, which matches the one-file shape of the PDF routes. `true` asks the engine to write images. The plaintext engine has none, so the flag does not change its response.

## Engine interface

Engines live outside this package. They import `github.com/gotenberg/gotenberg/v8/pkg/modules/markdown`. This package does not import them.

```go
type Flavour string

const (
    FlavourGFM        Flavour = "gfm"
    FlavourCommonMark Flavour = "commonmark"
)

type Options struct {
    Flavour       Flavour
    ExtractImages bool
    ImageDir      string
    Filename      string
}

type MarkdownEngine interface {
    Extensions() []string
    Convert(ctx context.Context, logger *slog.Logger, inputPath, outputPath string, options Options) error
}

type Provider interface {
    Markdown() (MarkdownEngine, error)
}
```

`Extensions` returns lower-case extensions with a leading dot, for example `.docx`.

`Convert` reads `inputPath` and creates the Markdown file at `outputPath`. `options.Filename` is the upload's original name. The registry matches on that extension when it is set, and on `inputPath` otherwise. The route always sets it, because the file on disk is a UUID plus the original extension.

`options.ImageDir` is empty when `ExtractImages` is false. The engine must not write companion files in that case. When `ExtractImages` is true, the route creates one directory per input and sets `ImageDir`. The engine writes image files directly in that directory, not in a subdirectory. Markdown links use the base name (`![alt](notes-1.png)`). The zip is flat, so prefix image names with the document stem. Two inputs that both emit `image1.png` would otherwise collide.

`ErrFormatNotSupported` is the sentinel for an extension the engine will not convert. The route answers 400 before calling `Convert` when nothing in the registry claims the extension.

`Provider` is how another module obtains the aggregated engine during `Provision`:

```go
mod, err := ctx.Module(new(markdown.Provider))
engine, err := mod.(markdown.Provider).Markdown()
err = engine.Convert(ctx, logger, inputPath, outputPath, markdown.Options{
    Flavour:  markdown.FlavourGFM,
    Filename: "notes.docx",
})
```

`Markdown()` returns a `*Registry`. `Lookup(".docx")` returns the selected engine. `Extensions()` lists the claimed extensions. `Convert` dispatches.

## How to add an engine

1. Implement `MarkdownEngine` and `gotenberg.Module` in a package that imports `pkg/modules/markdown`. Do not make `pkg/modules/markdown` import that package.
2. Register from `init` with `gotenberg.MustRegisterModule`.
3. Blank-import the package from `pkg/standard/imports.go`. Also add it to `pkg/standard/chromium/imports.go` and `pkg/standard/libreoffice/imports.go` when the engine does not depend on the variant that file excludes.
4. Return the extensions from `Extensions`. The module ID is the name operators pass to `--markdown-engines`.
5. Shell out with `gotenberg.CommandContext` so a cancelled request kills the process. Start a client span:

```go
ctx, span := gotenberg.Tracer().Start(ctx, "docx.Convert",
    trace.WithSpanKind(trace.SpanKindClient),
    trace.WithAttributes(semconv.ServerAddress(binPath)),
)
defer span.End()
```

The plaintext engine does not do this. It copies bytes in process and has no external tool.

A new option is a field on `Options` whose zero value keeps today's behavior. Do not rename `flavour`, `extractImages`, or the route.

## Recommended engine per input

The goal is a small dependency set and a Docker image that stays close to today's size. Pandoc is the best single converter and the wrong first dependency: the binary plus its data files add a Haskell runtime on the order of 100MB, and a second process to supervise. Reach for it only after the path below misses a quality bar the owning unit writes down (tables, headings, links, lists, images).

### Office: docx, xlsx, pptx, odt, rtf

Use LibreOffice to export HTML, then a Go HTML-to-Markdown library.

LibreOffice is already in the image and already opens these formats. HTML keeps headings, lists, tables, links, and images, which is the structure Markdown needs. LibreOffice does not have a Markdown export worth using.

`github.com/JohannesKaufmann/html-to-markdown` is pure Go, supports GFM tables, and does not enlarge the image. The same library is the HTML engine, so the office unit and the HTML unit share one implementation.

Chromium does not open Office documents. Do not route them through it.

### HTML

Use the same Go library on the uploaded HTML. It is deterministic and it does not start a browser.

Chromium is the tool for a later case: a URL whose content depends on JavaScript. The flow there is Chromium rendering to HTML (`document.documentElement.outerHTML`), then the same library. That is not the file upload path, and it is not required to convert `.html` or `.htm`.

`github.com/gomarkdown/markdown`, already in the module graph, converts Markdown to HTML. It does not help in this direction.

### PDF

Use Poppler `pdftotext -markdown` from the `poppler-utils` package.

Poppler is a few megabytes. Current releases emit Markdown directly, which is the right tool for a text PDF. `pdfcpu` is already in the image and is a poor text extractor. LibreOffice can import a PDF and the result is a drawing, not a document. Pandoc's PDF reader shells out to Poppler and adds little.

If tables are too weak, the fallback is Poppler `pdftohtml` and the same Go HTML-to-Markdown library. Measure that before adding it. The PDF unit adds the package. This unit does not.

### Plain text, this unit

The `plaintext` module (ID `plaintext`) copies `.txt`, `.text`, `.md`, and `.markdown`. The bytes are unchanged. A `.txt` file is Markdown with no markup. A `.md` file is already Markdown. Flavour and `extractImages` are accepted and ignored.

### Images and OCR

Out of scope. OCR needs an engine such as Tesseract, plus a layout pass, and it belongs in its own unit. The interface can take `.png`, `.jpg`, and `.tiff` later. Do not add Tesseract in the office, HTML, or PDF units.

## What the next units own

- Office: an engine module whose extensions are `.docx`, `.xlsx`, `.pptx`, `.odt`, and `.rtf`. It does not change the route.
- HTML: an engine for `.html` and `.htm`.
- PDF: an engine for `.pdf`, and `poppler-utils` in the image.

The integration scenario `test/integration/features/markdown_convert.feature` treats `.xyz` as unsupported so those units do not have to edit it. A unit that wants a new form field adds it with a default that preserves the current response.

## PDF engine

The `pdf` module converts `.pdf`. The package is `pkg/modules/pdfmarkdown`. The module ID is `pdf`.

Poppler in this image is 25.03.0, from Debian 13 `poppler-utils`. `pdftotext -markdown` is not a flag in that release. The manual's option list ends at `-h`. The engine runs `pdftohtml -xml -zoom 1` and builds Markdown in Go from the font size and the position of each text run. `pdftohtml -s` emits absolutely positioned HTML, where headings and tables are not elements.

A line whose font is at least 1.15 times the body size, and at most 14 words, becomes a heading. The largest sizes are `#`, then `##`, then `###`. A wide vertical gap splits the page into columns, and each column is read from top to bottom before the next one. That is what fixes a PDF whose content stream stores the columns row by row. Two or more short aligned rows become a table. `gfm` emits a pipe table. `commonmark` emits an HTML table. A line that starts with `-`, `*`, a bullet, or `1.` becomes a list. Wrapped lines with the same font and indent join into a paragraph. A line that ends with `-` joins the next line when that line starts with a lowercase letter.

Pages are separated by `---`. `--pdf-markdown-page-separator` (env `PDF_MARKDOWN_PAGE_SEPARATOR`) replaces that marker. An empty value leaves a blank line. The two characters `\n` in the value become a newline.

The default `extractImages` value is false. The engine then passes `-i` and writes no images. `true` keeps Poppler's images, copies them into `ImageDir`, and links `![](stem-1.jpg)`. Names use the upload stem so two PDFs in one request do not collide.

`PDFTOHTML_BIN_PATH` is the absolute path of `pdftohtml`. The image sets `/usr/bin/pdftohtml`. The request context is the timeout. `gotenberg.CommandContext` kills the process group when the request is cancelled, same as the PDF engines. The client span is `pdf.Convert`.

A trailer that contains `/Encrypt` is HTTP 400. The body is `Invalid 'files' form field: '<name>' is encrypted. Remove the password and try again.` An owner password with an empty user password is rejected too, even when Poppler could still read the text. A PDF with no letters or digits is HTTP 400. The body is `Invalid 'files' form field: '<name>' has no extractable text. Scanned pages need OCR, which this engine does not do.` Neither case writes an empty Markdown file.

`poppler-utils` 25.03.0 is 723 KiB installed. `libpoppler147` is 4857 KiB installed. The Dockerfile installs them with `--no-install-recommends`, so `poppler-data` is absent. That recommendation is the CJK CMap set and is several times the size of the library. Chromium already supplies Cairo, NSS, fontconfig, FreeType, JPEG, and PNG, so the full image grows by libpoppler, the utils, and a few codecs Chromium does not ship (OpenJPEG, libtiff, Little CMS, gpgmepp). That is on the order of 8 to 12 MiB. This tree was checked without a Docker build. The figure is the Debian package installed size.

Limits:

- Heading level is a size ratio. The PDF outline is ignored.
- A two-column page of long lines is read column by column. A grid of short cells is a table. Long sentences inside table cells can be read as columns.
- A scanned page that still has a text page number is converted.
- A content stream that contains the bytes `/Encrypt` followed by an object reference is rejected.
- Rotated text, running headers, and footers are ordinary lines.
- There is no OCR.
- CJK text is weaker without `poppler-data`.
