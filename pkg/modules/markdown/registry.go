package markdown

import (
	"context"
	"fmt"
	"log/slog"
	"sort"

	"github.com/gotenberg/gotenberg/v8/pkg/gotenberg"
)

// Registry maps a file extension to the engine that converts it.
//
// The first engine in the slice that claims an extension wins. The markdown
// module passes engines in --markdown-engines order (module ID order when
// the flag is empty) so an operator can prefer one implementation. A second
// claim is not a startup error: two modules may list the same extension
// while only one of them is selected.
type Registry struct {
	byExt map[string]MarkdownEngine
}

// NewRegistry builds a registry from engines, in priority order.
// Nil entries are skipped.
func NewRegistry(engines []MarkdownEngine) *Registry {
	registry := &Registry{
		byExt: make(map[string]MarkdownEngine),
	}

	for _, engine := range engines {
		if engine == nil {
			continue
		}

		for _, ext := range engine.Extensions() {
			ext = normalizeExt(ext)
			if ext == "" {
				continue
			}

			if _, exists := registry.byExt[ext]; exists {
				continue
			}

			registry.byExt[ext] = engine
		}
	}

	return registry
}

// Extensions returns the claimed extensions, sorted, each with a leading dot.
func (r *Registry) Extensions() []string {
	if r == nil {
		return nil
	}

	exts := make([]string, 0, len(r.byExt))
	for ext := range r.byExt {
		exts = append(exts, ext)
	}

	sort.Strings(exts)

	return exts
}

// Lookup returns the engine that claims ext.
// ext may be ".TXT" or "txt".
func (r *Registry) Lookup(ext string) (MarkdownEngine, bool) {
	if r == nil {
		return nil, false
	}

	engine, ok := r.byExt[normalizeExt(ext)]

	return engine, ok
}

// Convert writes Markdown for one file, using the engine that claims the
// extension of options.Filename, or of inputPath when Filename is empty.
func (r *Registry) Convert(ctx context.Context, logger *slog.Logger, inputPath, outputPath string, options Options) error {
	if r == nil {
		return fmt.Errorf("no markdown engine: %w", ErrFormatNotSupported)
	}

	ext := inputExt(inputPath, options)
	engine, ok := r.Lookup(ext)
	if !ok {
		label := ext
		if label == "" {
			label = "(none)"
		}

		return fmt.Errorf("extension %s: %w", label, ErrFormatNotSupported)
	}

	if logger != nil && ctx != nil {
		if mod, isModule := engine.(gotenberg.Module); isModule {
			logger.DebugContext(ctx, fmt.Sprintf("markdown engine '%s' converts '%s'", mod.Descriptor().ID, ext))
		}
	}

	err := engine.Convert(ctx, logger, inputPath, outputPath, options)
	if err != nil {
		return fmt.Errorf("convert %s to markdown: %w", ext, err)
	}

	return nil
}

// Interface guard.
var _ MarkdownEngine = (*Registry)(nil)
