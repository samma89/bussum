package htmlmarkdown

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"golang.org/x/net/html"

	"github.com/gotenberg/gotenberg/v8/pkg/modules/markdown"
)

const (
	maxExtractedImages = 200
	maxImageBytes      = 32 << 20
	maxExtractedBytes  = 64 << 20
	maxStemRunes       = 80
)

var (
	errNotLocalImage = errors.New("not a local image")
	errImageOutside  = errors.New("image is outside the input directory")
)

var imageExtensions = map[string]struct{}{
	".png":  {},
	".jpg":  {},
	".jpeg": {},
	".gif":  {},
	".webp": {},
	".svg":  {},
	".bmp":  {},
	".tif":  {},
	".tiff": {},
	".ico":  {},
	".avif": {},
	".apng": {},
}

func extractImages(ctx context.Context, logger *slog.Logger, doc *html.Node, inputPath string, options markdown.Options) error {
	stem := documentStem(options.Filename, inputPath)
	count := 0
	total := 0

	var walk func(*html.Node) error
	walk = func(node *html.Node) error {
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			if err := ctx.Err(); err != nil {
				return err
			}

			if child.Type == html.ElementNode && strings.EqualFold(child.Data, "img") {
				err := rewriteImage(ctx, logger, child, inputPath, options.ImageDir, stem, &count, &total)
				if err != nil {
					return err
				}
			}

			err := walk(child)
			if err != nil {
				return err
			}
		}

		return nil
	}

	err := walk(doc)
	if err != nil {
		return fmt.Errorf("extract images: %w", err)
	}

	if logger != nil {
		logger.DebugContext(ctx, fmt.Sprintf("extracted %d images", count))
	}

	return nil
}

func rewriteImage(ctx context.Context, logger *slog.Logger, node *html.Node, inputPath, imageDir, stem string, count, total *int) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	src := strings.TrimSpace(nodeAttr(node, "src"))
	if src == "" {
		return nil
	}

	ext, data, handled, err := decodeDataImage(src)
	if handled {
		if err != nil {
			// A broken data URI stays in the Markdown. The rest of the
			// document is still worth converting.
			if logger != nil {
				logger.DebugContext(ctx, fmt.Sprintf("skip data image: %s", err))
			}

			return nil
		}

		name, writeErr := writeExtracted(imageDir, stem, ext, data, count, total)
		if writeErr != nil {
			return writeErr
		}

		setNodeAttr(node, "src", name)

		return nil
	}

	path, err := resolveLocalImage(inputPath, src)
	if err != nil {
		if errors.Is(err, errNotLocalImage) {
			return nil
		}

		if errors.Is(err, errImageOutside) {
			if logger != nil {
				logger.DebugContext(ctx, "skip image outside the input directory")
			}

			return nil
		}

		return err
	}

	// The path was checked against the input directory above.
	data, err = os.ReadFile(path) //nolint:gosec
	if err != nil {
		return fmt.Errorf("read image: %w", err)
	}

	name, err := writeExtracted(imageDir, stem, strings.ToLower(filepath.Ext(path)), data, count, total)
	if err != nil {
		return err
	}

	setNodeAttr(node, "src", name)

	return nil
}

func writeExtracted(dir, stem, ext string, data []byte, count, total *int) (string, error) {
	if *count >= maxExtractedImages {
		return "", fmt.Errorf("extracted image count exceeds %d", maxExtractedImages)
	}

	if len(data) > maxImageBytes {
		return "", fmt.Errorf("image is %d bytes, the limit is %d", len(data), maxImageBytes)
	}

	if *total+len(data) > maxExtractedBytes {
		return "", fmt.Errorf("extracted images exceed %d bytes", maxExtractedBytes)
	}

	*count++
	name := fmt.Sprintf("%s-%d%s", stem, *count, ext)
	if filepath.Base(name) != name {
		return "", fmt.Errorf("image name %q escapes the image directory", name)
	}

	err := os.WriteFile(filepath.Join(dir, name), data, 0o600) //nolint:gosec
	if err != nil {
		return "", fmt.Errorf("write image: %w", err)
	}

	*total += len(data)

	return name, nil
}

func decodeDataImage(src string) (ext string, data []byte, handled bool, err error) {
	if len(src) < len("data:") || !strings.EqualFold(src[:len("data:")], "data:") {
		return "", nil, false, nil
	}

	rest := src[len("data:"):]
	before, after, ok := strings.Cut(rest, ",")
	if !ok {
		return "", nil, true, errors.New("data URI has no payload")
	}

	meta := before
	payload := after
	mediaType := meta
	base64Encoded := false

	parts := strings.Split(meta, ";")
	if len(parts) > 0 {
		mediaType = parts[0]
		for _, part := range parts[1:] {
			if strings.EqualFold(strings.TrimSpace(part), "base64") {
				base64Encoded = true
			}
		}
	}

	mediaType = strings.ToLower(strings.TrimSpace(mediaType))
	if mediaType == "" {
		mediaType = "text/plain"
	}

	if !strings.HasPrefix(mediaType, "image/") {
		return "", nil, false, nil
	}

	ext = extensionForMedia(mediaType)
	if ext == "" {
		return "", nil, false, nil
	}

	if base64Encoded {
		payload = strings.Map(func(r rune) rune {
			switch r {
			case ' ', '\n', '\r', '\t':
				return -1
			default:
				return r
			}
		}, payload)

		data, err = base64.StdEncoding.DecodeString(payload)
		if err != nil {
			data, err = base64.RawStdEncoding.DecodeString(payload)
		}
	} else {
		var decoded string
		decoded, err = url.PathUnescape(payload)
		data = []byte(decoded)
	}

	if err != nil {
		return "", nil, true, fmt.Errorf("decode data URI: %w", err)
	}

	return ext, data, true, nil
}

func extensionForMedia(media string) string {
	switch media {
	case "image/png":
		return ".png"
	case "image/jpeg", "image/jpg":
		return ".jpg"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	case "image/svg+xml":
		return ".svg"
	case "image/bmp":
		return ".bmp"
	case "image/tiff":
		return ".tiff"
	case "image/x-icon", "image/vnd.microsoft.icon":
		return ".ico"
	case "image/avif":
		return ".avif"
	default:
		return ""
	}
}

func resolveLocalImage(inputPath, src string) (string, error) {
	if strings.TrimSpace(inputPath) == "" {
		return "", errNotLocalImage
	}

	raw := src
	if i := strings.IndexAny(raw, "?#"); i >= 0 {
		raw = raw[:i]
	}

	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errNotLocalImage
	}

	lower := strings.ToLower(raw)
	switch {
	case strings.HasPrefix(lower, "http://"), strings.HasPrefix(lower, "https://"), strings.HasPrefix(lower, "data:"):
		return "", errNotLocalImage
	}

	var path string
	switch {
	case strings.HasPrefix(lower, "file:"):
		parsed, err := url.Parse(raw)
		if err != nil || parsed.Path == "" {
			return "", errNotLocalImage
		}

		path = parsed.Path
	case strings.Contains(raw, "://"):
		return "", errNotLocalImage
	default:
		unescaped, err := url.PathUnescape(raw)
		if err != nil {
			unescaped = raw
		}

		if filepath.IsAbs(unescaped) {
			path = unescaped
		} else {
			path = filepath.Join(filepath.Dir(inputPath), filepath.FromSlash(unescaped))
		}
	}

	path = filepath.Clean(path)
	root := filepath.Clean(filepath.Dir(inputPath))
	if !withinDir(root, path) {
		return "", errImageOutside
	}

	ext := strings.ToLower(filepath.Ext(path))
	if _, ok := imageExtensions[ext]; !ok {
		return "", errNotLocalImage
	}

	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", errNotLocalImage
		}

		return "", fmt.Errorf("resolve image: %w", err)
	}

	resolved = filepath.Clean(resolved)
	if !withinDir(root, resolved) {
		return "", errImageOutside
	}

	info, err := os.Stat(resolved) //nolint:gosec // resolved path is confined to the input directory
	if err != nil {
		if os.IsNotExist(err) {
			return "", errNotLocalImage
		}

		return "", fmt.Errorf("stat image: %w", err)
	}

	if !info.Mode().IsRegular() {
		return "", errNotLocalImage
	}

	if info.Size() > maxImageBytes {
		return "", fmt.Errorf("image file is %d bytes, the limit is %d", info.Size(), maxImageBytes)
	}

	return resolved, nil
}

func withinDir(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}

	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func documentStem(filename, inputPath string) string {
	name := strings.TrimSpace(filename)
	if name == "" {
		name = filepath.Base(inputPath)
	}

	stem := strings.TrimSuffix(filepath.Base(name), filepath.Ext(name))
	stem = strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '.' || r == '_' || r == '-' {
			return r
		}

		return '-'
	}, stem)
	stem = strings.Trim(stem, ".- ")

	runes := []rune(stem)
	if len(runes) > maxStemRunes {
		stem = strings.Trim(string(runes[:maxStemRunes]), ".- ")
	}

	if stem == "" {
		return "document"
	}

	return stem
}
