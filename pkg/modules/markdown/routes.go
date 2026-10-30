package markdown

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/labstack/echo/v5"

	"github.com/gotenberg/gotenberg/v8/pkg/modules/api"
)

// convertRoute returns the multipart route that converts uploaded files to
// Markdown. One file comes back as a Markdown file. Several outputs (more
// than one input, or a Markdown file plus extracted images) come back as a
// zip, which is how the API builds every multi-file response.
func convertRoute(registry *Registry, defaults Options) api.Route {
	return api.Route{
		Method:      http.MethodPost,
		Path:        ConvertRoutePath,
		IsMultipart: true,
		Handler: func(c *echo.Context) error {
			ctx := c.Get("context").(*api.Context)
			form := ctx.FormData()

			options := defaults
			err := form.
				Custom("flavour", func(value string) error {
					if strings.TrimSpace(value) == "" {
						options.Flavour = defaults.Flavour
						return nil
					}

					flavour, parseErr := ParseFlavour(value)
					if parseErr != nil {
						return parseErr
					}

					options.Flavour = flavour
					return nil
				}).
				Bool("extractImages", &options.ExtractImages, defaults.ExtractImages).
				Validate()
			if err != nil {
				return fmt.Errorf("validate form data: %w", err)
			}

			var inputPaths []string
			form.AllPaths(&inputPaths)

			if len(inputPaths) == 0 {
				return api.WrapError(
					errors.New("no input file"),
					api.NewSentinelHttpError(http.StatusBadRequest, noInputFileMessage),
				)
			}

			bad := unsupportedExtensions(registry, ctx, inputPaths)
			if len(bad) > 0 {
				return api.WrapError(
					fmt.Errorf("unsupported markdown extensions: %s", strings.Join(bad, ", ")),
					api.NewSentinelHttpError(http.StatusBadRequest, unsupportedMessage(bad, registry.Extensions())),
				)
			}

			outputs, err := convertInputs(ctx, registry, inputPaths, options)
			if err != nil {
				return err
			}

			err = ctx.AddOutputPaths(outputs...)
			if err != nil {
				return fmt.Errorf("add output paths: %w", err)
			}

			return nil
		},
	}
}

const noInputFileMessage = "Invalid 'files' form field: at least one file is required."

func unsupportedExtensions(registry *Registry, ctx *api.Context, inputPaths []string) []string {
	seen := make(map[string]struct{})
	var bad []string

	for _, inputPath := range inputPaths {
		ext := inputExt(inputPath, Options{Filename: ctx.OriginalFilename(inputPath)})
		if _, ok := registry.Lookup(ext); ok {
			continue
		}

		label := extLabel(ext)
		if _, ok := seen[label]; ok {
			continue
		}

		seen[label] = struct{}{}
		bad = append(bad, label)
	}

	sort.Strings(bad)

	return bad
}

func unsupportedMessage(exts, valid []string) string {
	kind := "extension"
	if len(exts) != 1 {
		kind = "extensions"
	}

	quoted := make([]string, len(exts))
	for i, ext := range exts {
		quoted[i] = "'" + ext + "'"
	}

	validList := strings.Join(valid, ", ")
	if validList == "" {
		validList = "(none)"
	}

	return fmt.Sprintf(
		"Invalid 'files' form field: unsupported %s %s. Valid extensions: %s.",
		kind,
		strings.Join(quoted, ", "),
		validList,
	)
}

func extLabel(ext string) string {
	if ext == "" {
		return "(none)"
	}

	return ext
}

func convertInputs(ctx *api.Context, registry *Registry, inputPaths []string, options Options) ([]string, error) {
	used := make(map[string]int)
	outputs := make([]string, 0, len(inputPaths))

	for i, inputPath := range inputPaths {
		filename := ctx.OriginalFilename(inputPath)
		fileOpts := options
		fileOpts.Filename = filename

		if options.ExtractImages {
			// One directory per input so two documents can use the same
			// image base name while they are still on disk. The archive
			// flattens them, which is why engines prefix image names with
			// the document stem.
			dir, err := ctx.CreateSubDirectory(fmt.Sprintf("markdown_images_%d", i))
			if err != nil {
				return nil, fmt.Errorf("create image directory: %w", err)
			}

			fileOpts.ImageDir = dir
		}

		// UUID on disk: the API symlinks the original filename in this
		// directory, and writing that name would overwrite the upload.
		outputPath := ctx.GeneratePathFromFilename(markdownOutputName(filename, used))

		err := registry.Convert(ctx, ctx.Log(), inputPath, outputPath, fileOpts)
		if err != nil {
			return nil, fmt.Errorf("convert '%s' to markdown: %w", filename, err)
		}

		outputs = append(outputs, outputPath)

		if fileOpts.ImageDir == "" {
			continue
		}

		images, err := imagePaths(fileOpts.ImageDir)
		if err != nil {
			return nil, err
		}

		outputs = append(outputs, images...)
	}

	return outputs, nil
}

func markdownOutputName(original string, used map[string]int) string {
	base := filepath.Base(original)
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	if stem == "" || stem == "." || stem == ".." {
		stem = "document"
	}

	name := stem + ".md"
	n := used[name]
	used[name]++

	if n == 0 {
		return name
	}

	return fmt.Sprintf("%s (%d).md", stem, n+1)
}

func imagePaths(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read extracted images: %w", err)
	}

	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		paths = append(paths, filepath.Join(dir, entry.Name()))
	}

	sort.Strings(paths)

	return paths, nil
}
