package markdown

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"

	flag "github.com/spf13/pflag"

	"github.com/gotenberg/gotenberg/v8/pkg/gotenberg"
	"github.com/gotenberg/gotenberg/v8/pkg/modules/api"
)

func init() {
	gotenberg.MustRegisterModule(new(Markdown))
}

// Markdown collects [MarkdownEngine] modules and serves the convert route.
type Markdown struct {
	// discovered is every engine module ID, sorted.
	discovered []string
	// engineNames is the operator's order, or discovered when the flag is
	// empty. It may name an engine that is not loaded. [Validate] reports
	// that.
	engineNames   []string
	disableRoutes bool
	defaults      Options
	registry      *Registry
}

// Descriptor returns a [Markdown]'s module descriptor.
func (mod *Markdown) Descriptor() gotenberg.ModuleDescriptor {
	return gotenberg.ModuleDescriptor{
		ID: "markdown",
		FlagSet: func() *flag.FlagSet {
			fs := flag.NewFlagSet("markdown", flag.ExitOnError)
			fs.StringSlice("markdown-engines", []string{}, "Set the Markdown engines and their order - empty means all. The first engine that claims an extension wins")
			fs.String("markdown-flavour", string(DefaultFlavour), "Set the default Markdown flavour - gfm or commonmark")
			fs.Bool("markdown-extract-images", false, "Extract embedded images next to the Markdown. The response is a zip when an engine writes images. Engines with no images ignore this")
			fs.Bool("markdown-disable-routes", false, "Disable the routes")

			return fs
		}(),
		New: func() gotenberg.Module { return new(Markdown) },
	}
}

// Provision loads the selected [MarkdownEngine] modules and the default
// conversion options.
func (mod *Markdown) Provision(ctx *gotenberg.Context) error {
	flags := ctx.ParsedFlags()
	mod.disableRoutes = flags.MustBool("markdown-disable-routes")
	mod.defaults = Options{
		Flavour:       Flavour(strings.ToLower(strings.TrimSpace(flags.MustString("markdown-flavour")))),
		ExtractImages: flags.MustBool("markdown-extract-images"),
	}

	mods, err := ctx.Modules(new(MarkdownEngine))
	if err != nil {
		return fmt.Errorf("get markdown engines: %w", err)
	}

	byID := make(map[string]MarkdownEngine, len(mods))
	ids := make([]string, 0, len(mods))

	for _, candidate := range mods {
		engine := candidate.(MarkdownEngine)
		// Every engine discovered this way is a module. The ID is the name
		// --markdown-engines selects.
		id := engine.(gotenberg.Module).Descriptor().ID
		byID[id] = engine
		ids = append(ids, id)
	}

	sort.Strings(ids)
	mod.discovered = ids
	mod.engineNames = ids
	if names := engineNames(flags.MustStringSlice("markdown-engines")); len(names) > 0 {
		mod.engineNames = names
	}

	ordered := make([]MarkdownEngine, 0, len(mod.engineNames))
	for _, id := range mod.engineNames {
		engine, ok := byID[id]
		if !ok {
			continue
		}

		ordered = append(ordered, engine)
	}

	mod.registry = NewRegistry(ordered)

	return nil
}

// Validate checks that the selected engines exist and the default flavour
// is one this module understands.
func (mod *Markdown) Validate() error {
	if _, err := ParseFlavour(string(mod.defaults.Flavour)); err != nil {
		return fmt.Errorf(
			"markdown flavour %q is invalid, expected %q or %q; set --markdown-flavour (env %s)",
			mod.defaults.Flavour,
			FlavourGFM,
			FlavourCommonMark,
			gotenberg.EnvVarName("markdown-flavour"),
		)
	}

	if len(mod.discovered) == 0 {
		return errors.New("no markdown engine is available; enable the plaintext module")
	}

	var missing []string

	for _, id := range mod.engineNames {
		if slices.Contains(mod.discovered, id) {
			continue
		}

		missing = append(missing, id)
	}

	if len(missing) == 0 {
		return nil
	}

	return fmt.Errorf(
		"non-existing markdown engine(s): %s; available markdown engine(s): %s; set --markdown-engines (env %s)",
		strings.Join(missing, ", "),
		strings.Join(mod.discovered, ", "),
		gotenberg.EnvVarName("markdown-engines"),
	)
}

// SystemMessages returns the selected engines, the extensions they claim,
// and the default conversion options.
func (mod *Markdown) SystemMessages() []string {
	engines := "(none)"
	if len(mod.engineNames) > 0 {
		engines = strings.Join(mod.engineNames, " ")
	}

	exts := "(none)"
	if mod.registry != nil && len(mod.registry.Extensions()) > 0 {
		exts = strings.Join(mod.registry.Extensions(), " ")
	}

	return []string{
		fmt.Sprintf("engines - %s", engines),
		fmt.Sprintf("extensions - %s", exts),
		fmt.Sprintf("flavour - %s", mod.defaults.Flavour),
		fmt.Sprintf("extract images - %t", mod.defaults.ExtractImages),
	}
}

// Debug returns the default options and the engine selected for each
// extension.
func (mod *Markdown) Debug() map[string]any {
	extensions := map[string]string{}

	if mod.registry != nil {
		for _, ext := range mod.registry.Extensions() {
			engine, ok := mod.registry.Lookup(ext)
			if !ok {
				continue
			}

			id := ext
			if asModule, isModule := engine.(gotenberg.Module); isModule {
				id = asModule.Descriptor().ID
			}

			extensions[ext] = id
		}
	}

	return map[string]any{
		"flavour":        string(mod.defaults.Flavour),
		"extract_images": mod.defaults.ExtractImages,
		"extensions":     extensions,
	}
}

// Markdown returns the aggregated [MarkdownEngine].
func (mod *Markdown) Markdown() (MarkdownEngine, error) {
	if mod.registry == nil {
		return nil, errors.New("markdown engine is not provisioned")
	}

	return mod.registry, nil
}

// Routes returns the convert route, or nothing when routes are disabled.
func (mod *Markdown) Routes() ([]api.Route, error) {
	if mod.disableRoutes {
		return nil, nil
	}

	if mod.registry == nil {
		return nil, errors.New("markdown engine is not provisioned")
	}

	return []api.Route{
		convertRoute(mod.registry, mod.defaults),
	}, nil
}

// engineNames trims the flag value. An empty item is what --markdown-engines=
// produces, and it means "all engines", not an engine with an empty name.
func engineNames(names []string) []string {
	seen := make(map[string]struct{}, len(names))
	out := make([]string, 0, len(names))

	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}

		if _, ok := seen[name]; ok {
			continue
		}

		seen[name] = struct{}{}
		out = append(out, name)
	}

	return out
}

// Interface guards.
var (
	_ gotenberg.Module       = (*Markdown)(nil)
	_ gotenberg.Provisioner  = (*Markdown)(nil)
	_ gotenberg.Validator    = (*Markdown)(nil)
	_ gotenberg.SystemLogger = (*Markdown)(nil)
	_ gotenberg.Debuggable   = (*Markdown)(nil)
	_ Provider               = (*Markdown)(nil)
	_ api.Router             = (*Markdown)(nil)
)
