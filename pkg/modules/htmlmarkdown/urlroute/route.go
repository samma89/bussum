package urlroute

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/labstack/echo/v5"

	"github.com/gotenberg/gotenberg/v8/pkg/gotenberg"
	"github.com/gotenberg/gotenberg/v8/pkg/modules/api"
	"github.com/gotenberg/gotenberg/v8/pkg/modules/chromium"
	"github.com/gotenberg/gotenberg/v8/pkg/modules/htmlmarkdown"
	"github.com/gotenberg/gotenberg/v8/pkg/modules/markdown"
)

func init() {
	gotenberg.MustRegisterModule(new(URL))
}

const (
	// RoutePath is the multipart route that renders a URL to Markdown.
	RoutePath = "/forms/markdown/convert/url"

	// defaultDocumentName is the Markdown filename when the caller does not
	// send Gotenberg-Output-Filename. The header replaces it the same way it
	// replaces any other single-file response.
	defaultDocumentName = "document.md"
)

// URL renders an HTTP page to Markdown.
//
// The file route stays POST /forms/markdown/convert. This route exists so a
// caller can omit files and still name a page. Chromium's allow-list,
// deny-list, and IP flags apply because rendering goes through
// [chromium.Api.HTML].
type URL struct {
	chromium      chromium.Api
	disableRoutes bool
	defaults      markdown.Options
}

// Descriptor returns a [URL]'s module descriptor.
func (mod *URL) Descriptor() gotenberg.ModuleDescriptor {
	return gotenberg.ModuleDescriptor{
		ID:  "htmlurl",
		New: func() gotenberg.Module { return new(URL) },
	}
}

// Provision loads Chromium and the Markdown flavour defaults.
//
// The flavour and extractImages flags belong to the markdown module. This
// module reads them so one process default covers both routes.
func (mod *URL) Provision(ctx *gotenberg.Context) error {
	flags := ctx.ParsedFlags()
	mod.disableRoutes = flags.MustBool("markdown-disable-routes")
	mod.defaults = markdown.Options{
		Flavour:       markdown.Flavour(strings.ToLower(strings.TrimSpace(flags.MustString("markdown-flavour")))),
		ExtractImages: flags.MustBool("markdown-extract-images"),
	}

	mods, err := ctx.Modules(new(chromium.Provider))
	if err != nil {
		return fmt.Errorf("get chromium module: %w", err)
	}

	if len(mods) != 1 {
		return errors.New("chromium module is not available; POST /forms/markdown/convert/url needs an image that includes Chromium")
	}

	browser, err := mods[0].(chromium.Provider).Chromium()
	if err != nil {
		return fmt.Errorf("get chromium api: %w", err)
	}

	mod.chromium = browser

	return nil
}

// Validate checks the default flavour.
func (mod *URL) Validate() error {
	if _, err := markdown.ParseFlavour(string(mod.defaults.Flavour)); err != nil {
		return fmt.Errorf(
			"markdown flavour %q is invalid, expected %q or %q; set --markdown-flavour (env %s)",
			mod.defaults.Flavour,
			markdown.FlavourGFM,
			markdown.FlavourCommonMark,
			gotenberg.EnvVarName("markdown-flavour"),
		)
	}

	return nil
}

// SystemMessages returns the route and the default conversion options.
func (mod *URL) SystemMessages() []string {
	route := "disabled"
	if !mod.disableRoutes && mod.chromium != nil {
		route = RoutePath
	}

	return []string{
		fmt.Sprintf("route - %s", route),
		fmt.Sprintf("flavour - %s", mod.defaults.Flavour),
		fmt.Sprintf("extract images - %t", mod.defaults.ExtractImages),
	}
}

// Debug returns the route state and the default conversion options.
func (mod *URL) Debug() map[string]any {
	return map[string]any{
		"route":          RoutePath,
		"enabled":        !mod.disableRoutes && mod.chromium != nil,
		"flavour":        string(mod.defaults.Flavour),
		"extract_images": mod.defaults.ExtractImages,
	}
}

// Routes returns the URL route, or nothing when routes are disabled.
func (mod *URL) Routes() ([]api.Route, error) {
	if mod.disableRoutes {
		return nil, nil
	}

	if mod.chromium == nil {
		return nil, errors.New("html url route is not provisioned")
	}

	return []api.Route{mod.route()}, nil
}

func (mod *URL) route() api.Route {
	return api.Route{
		Method:      http.MethodPost,
		Path:        RoutePath,
		IsMultipart: true,
		Handler: func(c *echo.Context) error {
			ctx := c.Get("context").(*api.Context)
			form, chromiumOptions := chromium.FormDataChromiumOptions(ctx)

			options := mod.defaults
			var pageURL string
			err := form.
				MandatoryString("url", &pageURL).
				Custom("flavour", func(value string) error {
					if strings.TrimSpace(value) == "" {
						options.Flavour = mod.defaults.Flavour
						return nil
					}

					flavour, parseErr := markdown.ParseFlavour(value)
					if parseErr != nil {
						return parseErr
					}

					options.Flavour = flavour
					return nil
				}).
				Bool("extractImages", &options.ExtractImages, mod.defaults.ExtractImages).
				Validate()
			if err != nil {
				return fmt.Errorf("validate form data: %w", err)
			}

			err = validateURL(pageURL)
			if err != nil {
				return fmt.Errorf("reject URL scheme: %w", err)
			}

			if mod.chromium == nil {
				return errors.New("chromium is not available")
			}

			page, err := mod.chromium.HTML(ctx, ctx.Log(), pageURL, chromiumOptions)
			err = chromium.HandleError(err, chromiumOptions)
			if err != nil {
				return fmt.Errorf("render url: %w", err)
			}

			options.Filename = defaultDocumentName
			outputPath := ctx.GeneratePathFromFilename(defaultDocumentName)

			if options.ExtractImages {
				dir, dirErr := ctx.CreateSubDirectory("markdown_images_0")
				if dirErr != nil {
					return fmt.Errorf("create image directory: %w", dirErr)
				}

				options.ImageDir = dir
			}

			err = htmlmarkdown.ConvertHTMLBytes(ctx, ctx.Log(), "", []byte(page), outputPath, options)
			if err != nil {
				return fmt.Errorf("convert rendered html to markdown: %w", err)
			}

			outputs := []string{outputPath}
			if options.ImageDir != "" {
				images, imageErr := imagePaths(options.ImageDir)
				if imageErr != nil {
					return imageErr
				}

				outputs = append(outputs, images...)
			}

			err = ctx.AddOutputPaths(outputs...)
			if err != nil {
				return fmt.Errorf("add output paths: %w", err)
			}

			return nil
		},
	}
}

// validateURL accepts only http and https.
//
// file:// would reach Chromium's working directory through the default
// deny-list's /tmp allowance. That allowance exists for HTML the other
// Chromium routes write themselves. This route takes a caller-supplied URL,
// so the scheme is rejected before Chromium navigates.
func validateURL(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return api.WrapError(
			fmt.Errorf("parse URL: %w", err),
			api.NewSentinelHttpError(http.StatusBadRequest, fmt.Sprintf("Invalid URL: %s", err)),
		)
	}

	switch strings.ToLower(parsed.Scheme) {
	case "http", "https":
		return nil
	case "file":
		return api.WrapError(
			errors.New("file scheme is not allowed on the markdown URL route"),
			api.NewSentinelHttpError(
				http.StatusBadRequest,
				"file:// URLs are not accepted on this route. Upload the HTML file to POST /forms/markdown/convert.",
			),
		)
	default:
		return api.WrapError(
			fmt.Errorf("%q scheme is not allowed on the markdown URL route", parsed.Scheme),
			api.NewSentinelHttpError(
				http.StatusBadRequest,
				"Invalid form data: form field 'url' must be an http:// or https:// URL",
			),
		)
	}
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

// Interface guards.
var (
	_ gotenberg.Module       = (*URL)(nil)
	_ gotenberg.Provisioner  = (*URL)(nil)
	_ gotenberg.Validator    = (*URL)(nil)
	_ gotenberg.SystemLogger = (*URL)(nil)
	_ gotenberg.Debuggable   = (*URL)(nil)
	_ api.Router             = (*URL)(nil)
)
