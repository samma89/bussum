package office

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/net/html"

	"github.com/gotenberg/gotenberg/v8/pkg/modules/markdown"
)

const maxImageBytes = 64 << 20

func rewriteImages(doc *html.Node, root string, options markdown.Options, stem string) error {
	var images []*html.Node
	collect(doc, "img", &images)

	written := 0

	for _, image := range images {
		err := rewriteImage(image, root, options, stem, &written)
		if err != nil {
			return err
		}
	}

	return nil
}

func rewriteImage(image *html.Node, root string, options markdown.Options, stem string, written *int) error {
	source := strings.TrimSpace(attr(image, "src"))
	alt := attr(image, "alt")

	if source == "" {
		replaceWithText(image, alt)
		return nil
	}

	if isRemoteImage(source) {
		return nil
	}

	data, ext, ok, err := imageBytes(root, source)
	if err != nil {
		return err
	}

	if !ok {
		replaceWithText(image, alt)
		return nil
	}

	if !options.ExtractImages {
		replaceWithText(image, alt)
		return nil
	}

	*written++
	name := fmt.Sprintf("%s-%d%s", stem, *written, ext)
	path := filepath.Join(options.ImageDir, name)

	err = os.WriteFile(path, data, 0o600) //nolint:gosec // ImageDir is the directory the route created for this input
	if err != nil {
		return fmt.Errorf("write extracted image %s: %w", name, err)
	}

	setAttr(image, "src", name)
	deleteAttr(image, "srcset")

	return nil
}

func imageBytes(root, source string) ([]byte, string, bool, error) {
	if isDataImage(source) {
		data, ext, ok := decodeDataImage(source)
		return data, ext, ok, nil
	}

	path, ok := resolveExportPath(root, source)
	if !ok {
		return nil, "", false, nil
	}

	file, err := os.Open(path) //nolint:gosec // path is constrained to the export directory
	if err != nil {
		if os.IsNotExist(err) {
			return nil, "", false, nil
		}

		return nil, "", false, fmt.Errorf("open exported image: %w", err)
	}

	defer func() {
		_ = file.Close()
	}()

	info, err := file.Stat()
	if err != nil {
		return nil, "", false, fmt.Errorf("stat exported image: %w", err)
	}

	if info.Size() > maxImageBytes {
		return nil, "", false, fmt.Errorf("exported image %s exceeds %d bytes", filepath.Base(path), maxImageBytes)
	}

	data, err := io.ReadAll(io.LimitReader(file, maxImageBytes+1))
	if err != nil {
		return nil, "", false, fmt.Errorf("read exported image: %w", err)
	}

	if int64(len(data)) > maxImageBytes {
		return nil, "", false, fmt.Errorf("exported image %s exceeds %d bytes", filepath.Base(path), maxImageBytes)
	}

	ext := imageExt(path, data)
	if ext == "" {
		return nil, "", false, nil
	}

	return data, ext, true, nil
}

func resolveExportPath(root, source string) (string, bool) {
	source = strings.TrimSpace(source)
	if parsed, err := url.Parse(source); err == nil && parsed.Scheme == "file" {
		source = parsed.Path
	}

	decoded, err := pathUnescape(source)
	if err != nil {
		return "", false
	}

	return withinRoot(root, decoded)
}

func withinRoot(root, source string) (string, bool) {
	cleaned := filepath.Clean(source)

	var absolute string
	if filepath.IsAbs(cleaned) {
		absolute = cleaned
	} else {
		absolute = filepath.Join(root, cleaned)
	}

	absolute = filepath.Clean(absolute)
	root = filepath.Clean(root)

	relative, err := filepath.Rel(root, absolute)
	if err != nil || escapes(relative) {
		return "", false
	}

	return absolute, true
}

func isRemoteImage(source string) bool {
	parsed, err := url.Parse(source)
	if err != nil {
		return false
	}

	return parsed.Scheme == "http" || parsed.Scheme == "https"
}

func isDataImage(source string) bool {
	return strings.HasPrefix(strings.ToLower(source), "data:")
}

func decodeDataImage(source string) ([]byte, string, bool) {
	comma := strings.Index(source, ",")
	if comma < 0 {
		return nil, "", false
	}

	meta := source[len("data:"):comma]
	payload := source[comma+1:]

	var data []byte
	var err error

	if strings.Contains(strings.ToLower(meta), ";base64") {
		data, err = base64.StdEncoding.DecodeString(payload)
		if err != nil {
			data, err = base64.RawStdEncoding.DecodeString(payload)
		}
	} else {
		decoded, unescapeErr := url.PathUnescape(payload)
		if unescapeErr != nil {
			return nil, "", false
		}

		data = []byte(decoded)
	}

	if err != nil || len(data) == 0 || len(data) > maxImageBytes {
		return nil, "", false
	}

	ext := extFromMeta(meta)
	if ext == "" {
		ext = imageExt("", data)
	}

	if ext == "" {
		return nil, "", false
	}

	return data, ext, true
}

func extFromMeta(meta string) string {
	low := strings.ToLower(meta)
	switch {
	case strings.Contains(low, "image/png"):
		return ".png"
	case strings.Contains(low, "image/jpeg"), strings.Contains(low, "image/jpg"):
		return ".jpg"
	case strings.Contains(low, "image/gif"):
		return ".gif"
	case strings.Contains(low, "image/webp"):
		return ".webp"
	case strings.Contains(low, "image/bmp"):
		return ".bmp"
	case strings.Contains(low, "image/svg"):
		return ".svg"
	case strings.Contains(low, "image/tiff"):
		return ".tiff"
	default:
		return ""
	}
}

func imageExt(name string, data []byte) string {
	ext := strings.ToLower(filepath.Ext(name))
	if isImageExt(ext) {
		return ext
	}

	contentType := http.DetectContentType(data)
	switch {
	case strings.Contains(contentType, "png"):
		return ".png"
	case strings.Contains(contentType, "jpeg"):
		return ".jpg"
	case strings.Contains(contentType, "gif"):
		return ".gif"
	case strings.Contains(contentType, "webp"):
		return ".webp"
	case strings.Contains(contentType, "bmp"):
		return ".bmp"
	case strings.Contains(contentType, "tiff"):
		return ".tiff"
	}

	sample := data
	if len(sample) > 256 {
		sample = sample[:256]
	}

	if bytes.Contains(bytes.ToLower(bytes.TrimSpace(sample)), []byte("<svg")) {
		return ".svg"
	}

	return ""
}

func isImageExt(ext string) bool {
	switch ext {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp", ".tif", ".tiff", ".svg", ".emf", ".wmf":
		return true
	default:
		return false
	}
}

// documentStem is the upload name used as the image prefix.
// The archive is flat, so the name has to be one path segment.
func documentStem(filename string) string {
	base := filepath.Base(strings.TrimSpace(filename))
	stem := strings.TrimSuffix(base, filepath.Ext(base))

	var builder strings.Builder
	lastDash := false

	for _, r := range stem {
		ok := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-'
		if r == ' ' || r == '\t' {
			r = '-'
			ok = true
		}

		if !ok {
			continue
		}

		if r == '-' {
			if lastDash || builder.Len() == 0 {
				continue
			}

			lastDash = true
		} else {
			lastDash = false
		}

		builder.WriteRune(r)
	}

	stem = strings.Trim(builder.String(), ".-")
	if stem == "" {
		return "document"
	}

	const maxStem = 80
	if len(stem) > maxStem {
		stem = strings.Trim(stem[:maxStem], ".-")
	}

	if stem == "" {
		return "document"
	}

	return stem
}
