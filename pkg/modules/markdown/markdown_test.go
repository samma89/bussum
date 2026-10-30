package markdown

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	flag "github.com/spf13/pflag"

	"github.com/gotenberg/gotenberg/v8/pkg/gotenberg"
)

func TestModulesRegistered(t *testing.T) {
	registered := make(map[string]bool)
	for _, desc := range gotenberg.GetModuleDescriptors() {
		registered[desc.ID] = true
	}

	for _, id := range []string{"markdown", "plaintext"} {
		if !registered[id] {
			t.Errorf("module %s is not registered", id)
		}
	}
}

type moduleEngine struct {
	id   string
	exts []string
}

func moduleEngineDescriptor(id string, exts []string) gotenberg.ModuleDescriptor {
	return gotenberg.ModuleDescriptor{
		ID: id,
		New: func() gotenberg.Module {
			return &moduleEngine{id: id, exts: exts}
		},
	}
}

func (e *moduleEngine) Descriptor() gotenberg.ModuleDescriptor {
	return moduleEngineDescriptor(e.id, e.exts)
}

func (e *moduleEngine) Extensions() []string { return e.exts }

func (e *moduleEngine) Convert(_ context.Context, _ *slog.Logger, _, outputPath string, options Options) error {
	dir := "0"
	if options.ImageDir != "" {
		dir = "1"
	}

	body := e.id + " " + string(options.Flavour) + " " + strconv.FormatBool(options.ExtractImages) + " " + dir + " " + options.Filename

	return os.WriteFile(outputPath, []byte(body), 0o600)
}

func provision(t *testing.T, args []string, engines []gotenberg.ModuleDescriptor) (*Markdown, error) {
	t.Helper()

	mod := new(Markdown)
	fs := flag.NewFlagSet("markdown", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.AddFlagSet(mod.Descriptor().FlagSet)

	err := fs.Parse(args)
	if err != nil {
		t.Fatalf("parse flags: %v", err)
	}

	ctx := gotenberg.NewContext(gotenberg.ParsedFlags{FlagSet: fs}, engines)
	err = mod.Provision(ctx)
	if err != nil {
		return nil, err
	}

	err = mod.Validate()
	if err != nil {
		return mod, err
	}

	return mod, nil
}

func TestMarkdown_Provision(t *testing.T) {
	plaintext := new(Plaintext).Descriptor()

	for _, tc := range []struct {
		name      string
		args      []string
		engines   []gotenberg.ModuleDescriptor
		wantErr   string
		wantDebug map[string]any
	}{
		{
			name:    "empty engine list means every engine",
			args:    []string{"--markdown-engines="},
			engines: []gotenberg.ModuleDescriptor{plaintext},
			wantDebug: map[string]any{
				"flavour":        "gfm",
				"extract_images": false,
			},
		},
		{
			name:    "upper case flavour is canonical",
			args:    []string{"--markdown-flavour=GFM", "--markdown-extract-images=true"},
			engines: []gotenberg.ModuleDescriptor{plaintext},
			wantDebug: map[string]any{
				"flavour":        "gfm",
				"extract_images": true,
			},
		},
		{
			name:    "invalid flavour",
			args:    []string{"--markdown-flavour=latex"},
			engines: []gotenberg.ModuleDescriptor{plaintext},
			wantErr: "MARKDOWN_FLAVOUR",
		},
		{
			name:    "unknown engine names the flag",
			args:    []string{"--markdown-engines=nope"},
			engines: []gotenberg.ModuleDescriptor{plaintext},
			wantErr: "MARKDOWN_ENGINES",
		},
		{
			name:    "no engine",
			engines: nil,
			wantErr: "plaintext",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mod, err := provision(t, tc.args, tc.engines)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatal("expected an error, got none")
				}

				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error = %q, want it to contain %q", err.Error(), tc.wantErr)
				}

				return
			}

			if err != nil {
				t.Fatalf("provision: %v", err)
			}

			debug := mod.Debug()
			if debug["flavour"] != tc.wantDebug["flavour"] {
				t.Fatalf("flavour = %v, want %v", debug["flavour"], tc.wantDebug["flavour"])
			}

			if debug["extract_images"] != tc.wantDebug["extract_images"] {
				t.Fatalf("extract_images = %v, want %v", debug["extract_images"], tc.wantDebug["extract_images"])
			}

			extensions, ok := debug["extensions"].(map[string]string)
			if !ok || extensions[".txt"] != "plaintext" || extensions[".md"] != "plaintext" {
				t.Fatalf("extensions = %v, want plaintext for .txt and .md", debug["extensions"])
			}
		})
	}
}

func TestMarkdown_EngineOrder(t *testing.T) {
	engines := []gotenberg.ModuleDescriptor{
		moduleEngineDescriptor("zeta", []string{".txt"}),
		moduleEngineDescriptor("alpha", []string{".txt", ".docx"}),
	}

	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{name: "module id order when the flag is empty", want: "alpha"},
		{name: "flag order", args: []string{"--markdown-engines=zeta,alpha"}, want: "zeta"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mod, err := provision(t, tc.args, engines)
			if err != nil {
				t.Fatalf("provision: %v", err)
			}

			engine, err := mod.Markdown()
			if err != nil {
				t.Fatalf("markdown: %v", err)
			}

			dir := t.TempDir()
			output := filepath.Join(dir, "out.md")
			err = engine.Convert(t.Context(), slog.New(slog.DiscardHandler), filepath.Join(dir, "blob"), output, Options{Filename: "notes.txt"})
			if err != nil {
				t.Fatalf("convert: %v", err)
			}

			body, err := os.ReadFile(output)
			if err != nil {
				t.Fatalf("read output: %v", err)
			}

			if !strings.HasPrefix(string(body), tc.want+" ") {
				t.Fatalf("engine output = %q, want it to start with %q", body, tc.want)
			}
		})
	}
}

func TestMarkdown_Routes(t *testing.T) {
	mod, err := provision(t, nil, []gotenberg.ModuleDescriptor{new(Plaintext).Descriptor()})
	if err != nil {
		t.Fatalf("provision: %v", err)
	}

	routes, err := mod.Routes()
	if err != nil {
		t.Fatalf("routes: %v", err)
	}

	if len(routes) != 1 {
		t.Fatalf("routes = %d, want 1", len(routes))
	}

	if routes[0].Path != ConvertRoutePath || routes[0].Method != "POST" || !routes[0].IsMultipart {
		t.Fatalf("route = %#v, want POST %s multipart", routes[0], ConvertRoutePath)
	}

	disabled, err := provision(t, []string{"--markdown-disable-routes=true"}, []gotenberg.ModuleDescriptor{new(Plaintext).Descriptor()})
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

func TestMarkdown_RouteUsesModuleDefaults(t *testing.T) {
	mod, err := provision(t, []string{
		"--markdown-flavour=commonmark",
		"--markdown-extract-images=true",
		"--markdown-engines=probe",
	}, []gotenberg.ModuleDescriptor{
		new(Plaintext).Descriptor(),
		moduleEngineDescriptor("probe", []string{".txt"}),
	})
	if err != nil {
		t.Fatalf("provision: %v", err)
	}

	routes, err := mod.Routes()
	if err != nil {
		t.Fatalf("routes: %v", err)
	}

	dir := t.TempDir()
	input := filepath.Join(dir, "hello.txt")
	err = os.WriteFile(input, []byte("hello"), 0o600)
	if err != nil {
		t.Fatalf("write input: %v", err)
	}

	mock := requestContext(t, dir, map[string]string{"hello.txt": input}, nil)
	err = callRoute(t, routes[0], mock)
	if err != nil {
		t.Fatalf("route: %v", err)
	}

	paths := mock.OutputPaths()
	if len(paths) != 1 {
		t.Fatalf("outputs = %d, want 1", len(paths))
	}

	body, err := os.ReadFile(paths[0])
	if err != nil {
		t.Fatalf("read output: %v", err)
	}

	const want = "probe commonmark true 1 hello.txt"
	if string(body) != want {
		t.Fatalf("output = %q, want %q", body, want)
	}

	if mock.OriginalFilename(paths[0]) != "hello.md" {
		t.Fatalf("filename = %q, want hello.md", mock.OriginalFilename(paths[0]))
	}
}
