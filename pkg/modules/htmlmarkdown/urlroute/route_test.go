package urlroute

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v5"
	flag "github.com/spf13/pflag"

	"github.com/gotenberg/gotenberg/v8/pkg/gotenberg"
	"github.com/gotenberg/gotenberg/v8/pkg/modules/api"
	"github.com/gotenberg/gotenberg/v8/pkg/modules/chromium"
	"github.com/gotenberg/gotenberg/v8/pkg/modules/markdown"
)

func TestModuleRegistered(t *testing.T) {
	for _, desc := range gotenberg.GetModuleDescriptors() {
		if desc.ID == "htmlurl" {
			return
		}
	}

	t.Fatal("htmlurl module is not registered")
}

func TestURL_Provision(t *testing.T) {
	mod, err := provisionURL(t, nil, true)
	if err != nil {
		t.Fatalf("provision: %v", err)
	}

	routes, err := mod.Routes()
	if err != nil {
		t.Fatalf("routes: %v", err)
	}

	if len(routes) != 1 || routes[0].Path != RoutePath {
		t.Fatalf("routes = %+v, want %s", routes, RoutePath)
	}

	_, err = provisionURL(t, nil, false)
	if err == nil {
		t.Fatal("expected an error when Chromium is not loaded")
	}

	_, err = provisionURL(t, []string{"--markdown-flavour=latex"}, true)
	if err == nil {
		t.Fatal("expected an error for an invalid flavour")
	}

	disabled, err := provisionURL(t, []string{"--markdown-disable-routes=true"}, true)
	if err != nil {
		t.Fatalf("provision disabled: %v", err)
	}

	routes, err = disabled.Routes()
	if err != nil {
		t.Fatalf("disabled routes: %v", err)
	}

	if len(routes) != 0 {
		t.Fatalf("disabled routes = %d, want 0", len(routes))
	}
}

func TestURLRoute_Render(t *testing.T) {
	var gotURL string
	var gotOptions chromium.Options

	mod := &URL{
		defaults: markdown.Options{Flavour: markdown.FlavourGFM},
		chromium: &chromium.ApiMock{
			HTMLMock: func(_ context.Context, _ *slog.Logger, pageURL string, options chromium.Options) (string, error) {
				gotURL = pageURL
				gotOptions = options
				return `<h1>Rendered by JavaScript</h1><script>alert(1)</script><nav>Secret nav</nav><del>gone</del>`, nil
			},
		},
	}

	dir := t.TempDir()
	mock := requestContext(t, dir, map[string][]string{
		"url":               {"https://example.com/page"},
		"waitDelay":         {"1500ms"},
		"waitForExpression": {"window.__ready === true"},
		"extraHttpHeaders":  {`{"X-Test":"yes"}`},
		"flavour":           {"commonmark"},
	})

	err := callRoute(t, mod.route(), mock)
	if err != nil {
		t.Fatalf("route: %v", err)
	}

	if gotURL != "https://example.com/page" {
		t.Fatalf("url = %q", gotURL)
	}

	if gotOptions.WaitDelay != 1500*time.Millisecond {
		t.Fatalf("waitDelay = %s", gotOptions.WaitDelay)
	}

	if gotOptions.WaitForExpression != "window.__ready === true" {
		t.Fatalf("waitForExpression = %q", gotOptions.WaitForExpression)
	}

	foundHeader := false
	for _, header := range gotOptions.ExtraHttpHeaders {
		if header.Name == "X-Test" && header.Value == "yes" {
			foundHeader = true
		}
	}

	if !foundHeader {
		t.Fatalf("extra headers = %+v", gotOptions.ExtraHttpHeaders)
	}

	paths := mock.OutputPaths()
	if len(paths) != 1 {
		t.Fatalf("outputs = %d, want 1", len(paths))
	}

	body, err := os.ReadFile(paths[0])
	if err != nil {
		t.Fatalf("read markdown: %v", err)
	}

	got := string(body)
	if !strings.Contains(got, "Rendered by JavaScript") {
		t.Fatalf("markdown = %s", got)
	}

	if strings.Contains(got, "alert") || strings.Contains(got, "Secret nav") {
		t.Fatalf("markdown kept noise: %s", got)
	}

	if strings.Contains(got, "~~gone~~") {
		t.Fatalf("commonmark request emitted strikethrough: %s", got)
	}

	if !strings.Contains(got, "gone") {
		t.Fatalf("markdown = %s", got)
	}
}

func TestURLRoute_Schemes(t *testing.T) {
	for _, tc := range []struct {
		name    string
		url     string
		want    int
		snippet string
	}{
		{name: "missing", want: http.StatusBadRequest, snippet: "form field 'url' is required"},
		{name: "file", url: "file:///tmp/page.html", want: http.StatusBadRequest, snippet: "file:// URLs are not accepted"},
		{name: "javascript", url: "javascript:alert(1)", want: http.StatusBadRequest, snippet: "http:// or https://"},
		{name: "empty scheme", url: "example.com", want: http.StatusBadRequest, snippet: "http:// or https://"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			mod := &URL{
				defaults: markdown.Options{Flavour: markdown.FlavourGFM},
				chromium: &chromium.ApiMock{
					HTMLMock: func(context.Context, *slog.Logger, string, chromium.Options) (string, error) {
						called = true
						return "", errors.New("chromium should not be called")
					},
				},
			}

			values := map[string][]string{}
			if tc.url != "" {
				values["url"] = []string{tc.url}
			}

			err := callRoute(t, mod.route(), requestContext(t, t.TempDir(), values))
			if err == nil {
				t.Fatal("expected an error")
			}

			if called {
				t.Fatal("chromium was called")
			}

			var httpErr api.HttpError
			if !errors.As(err, &httpErr) {
				t.Fatalf("error = %v, want an HTTP error", err)
			}

			status, message := httpErr.HttpError()
			if status != tc.want {
				t.Fatalf("status = %d, want %d (%s)", status, tc.want, message)
			}

			if !strings.Contains(message, tc.snippet) {
				t.Fatalf("message = %q, want it to contain %q", message, tc.snippet)
			}
		})
	}
}

func TestURLRoute_WaitExpression(t *testing.T) {
	mod := &URL{
		defaults: markdown.Options{Flavour: markdown.FlavourGFM},
		chromium: &chromium.ApiMock{
			HTMLMock: func(context.Context, *slog.Logger, string, chromium.Options) (string, error) {
				return "", errors.New("evaluate: boom")
			},
		},
	}

	err := callRoute(t, mod.route(), requestContext(t, t.TempDir(), map[string][]string{
		"url":               {"https://example.com"},
		"waitForExpression": {"window.__ready === true"},
	}))
	if err == nil {
		t.Fatal("expected an error")
	}

	// The expression itself did not fail inside Chromium. A generic render
	// failure stays an internal error, and the client does not see the URL.
	var httpErr api.HttpError
	if errors.As(err, &httpErr) {
		t.Fatalf("error = %v, want an internal error", err)
	}

	if strings.Contains(err.Error(), "example.com") {
		t.Fatalf("error leaks the URL: %v", err)
	}
}

func TestURLRoute_InvalidWaitExpression(t *testing.T) {
	mod := &URL{
		defaults: markdown.Options{Flavour: markdown.FlavourGFM},
		chromium: &chromium.ApiMock{
			HTMLMock: func(context.Context, *slog.Logger, string, chromium.Options) (string, error) {
				return "", errors.Join(errors.New("evaluate"), chromium.ErrInvalidEvaluationExpression)
			},
		},
	}

	err := callRoute(t, mod.route(), requestContext(t, t.TempDir(), map[string][]string{
		"url":               {"https://example.com"},
		"waitForExpression": {"not javascript"},
	}))
	if err == nil {
		t.Fatal("expected an error")
	}

	var httpErr api.HttpError
	if !errors.As(err, &httpErr) {
		t.Fatalf("error = %v, want an HTTP error", err)
	}

	status, message := httpErr.HttpError()
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (%s)", status, message)
	}

	if !strings.Contains(message, "waitForExpression") {
		t.Fatalf("message = %q", message)
	}
}

type chromiumStub struct {
	api chromium.Api
}

func (s *chromiumStub) Descriptor() gotenberg.ModuleDescriptor {
	return gotenberg.ModuleDescriptor{
		ID:  "chromium-stub",
		New: func() gotenberg.Module { return s },
	}
}

func (s *chromiumStub) Chromium() (chromium.Api, error) {
	if s.api == nil {
		s.api = &chromium.ApiMock{
			HTMLMock: func(context.Context, *slog.Logger, string, chromium.Options) (string, error) {
				return "<p>ok</p>", nil
			},
		}
	}

	return s.api, nil
}

func provisionURL(t *testing.T, args []string, withChromium bool) (*URL, error) {
	t.Helper()

	fs := flag.NewFlagSet("urlroute", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.AddFlagSet(new(markdown.Markdown).Descriptor().FlagSet)

	err := fs.Parse(args)
	if err != nil {
		t.Fatalf("parse flags: %v", err)
	}

	var descriptors []gotenberg.ModuleDescriptor
	if withChromium {
		descriptors = append(descriptors, (&chromiumStub{}).Descriptor())
	}

	mod := new(URL)
	ctx := gotenberg.NewContext(gotenberg.ParsedFlags{FlagSet: fs}, descriptors)
	err = mod.Provision(ctx)
	if err != nil {
		return nil, err
	}

	err = mod.Validate()
	if err != nil {
		return nil, err
	}

	return mod, nil
}

func requestContext(t *testing.T, dir string, values map[string][]string) *api.ContextMock {
	t.Helper()

	ctx := &api.Context{}
	mock := &api.ContextMock{Context: ctx}
	mock.SetDirPath(dir)
	mock.SetValues(values)
	mock.SetLogger(slog.New(slog.DiscardHandler))
	mock.SetMkdirAll(new(gotenberg.OsMkdirAll))
	ctx.Context = t.Context()

	return mock
}

func callRoute(t *testing.T, route api.Route, mock *api.ContextMock) error {
	t.Helper()

	c := echo.New().NewContext(
		httptest.NewRequest(http.MethodPost, RoutePath, nil),
		httptest.NewRecorder(),
	)
	c.Set("context", mock.Context)

	return route.Handler(c)
}

func TestURLRoute_OutputFile(t *testing.T) {
	mod := &URL{
		defaults: markdown.Options{Flavour: markdown.FlavourGFM, ExtractImages: false},
		chromium: &chromium.ApiMock{
			HTMLMock: func(context.Context, *slog.Logger, string, chromium.Options) (string, error) {
				return "<h1>Hi</h1>", nil
			},
		},
	}

	dir := t.TempDir()
	mock := requestContext(t, dir, map[string][]string{
		"url": {"https://example.com"},
	})

	err := callRoute(t, mod.route(), mock)
	if err != nil {
		t.Fatalf("route: %v", err)
	}

	paths := mock.OutputPaths()
	if len(paths) != 1 {
		t.Fatalf("outputs = %v", paths)
	}

	if filepath.Base(paths[0]) == "" {
		t.Fatal("empty output name")
	}

	if !strings.HasPrefix(paths[0], dir) {
		t.Fatalf("output %s is outside %s", paths[0], dir)
	}
}
