package office

import (
	"archive/zip"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

const (
	// maxSheetNameBytes caps how far a workbook is scanned for sheet names.
	// The names sit on the sheet elements. A hostile package cannot force an
	// unbounded read.
	maxSheetNameBytes = 32 << 20
	maxSheetNames     = 256
)

// errSheetNamesTruncated is returned with the names collected before the cap.
var errSheetNamesTruncated = errors.New("sheet name scan truncated")

// sheetNames reads sheet titles from an OOXML or ODS package.
// Other extensions, including legacy .xls, return no names.
func sheetNames(path, ext string) ([]string, error) {
	switch ext {
	case ".xlsx":
		return namesFromZip(path, "xl/workbook.xml", "sheets", "sheet")
	case ".ods":
		return namesFromZip(path, "content.xml", "spreadsheet", "table")
	default:
		return nil, nil
	}
}

func namesFromZip(path, entry, parent, element string) ([]string, error) {
	file, err := os.Open(path) //nolint:gosec // path is the uploaded document
	if err != nil {
		return nil, fmt.Errorf("open office package: %w", err)
	}

	defer func() {
		_ = file.Close()
	}()

	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat office package: %w", err)
	}

	reader, err := zip.NewReader(file, info.Size())
	if err != nil {
		return nil, fmt.Errorf("read office package: %w", err)
	}

	entryReader, err := reader.Open(entry)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", entry, err)
	}

	defer func() {
		_ = entryReader.Close()
	}()

	names, err := decodeSheetNames(io.LimitReader(entryReader, maxSheetNameBytes+1), parent, element)
	if err != nil && len(names) > 0 && errors.Is(err, errSheetNamesTruncated) {
		return names, nil
	}

	if err != nil {
		return nil, err
	}

	return names, nil
}

func decodeSheetNames(r io.Reader, parent, element string) ([]string, error) {
	decoder := xml.NewDecoder(r)
	var stack []string
	var names []string
	var read int

	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			return names, nil
		}

		if err != nil {
			return names, fmt.Errorf("parse office xml: %w", err)
		}

		switch typed := token.(type) {
		case xml.StartElement:
			if typed.Name.Local == element && len(stack) > 0 && stack[len(stack)-1] == parent {
				if name := attrLocal(typed.Attr, "name"); name != "" {
					names = append(names, name)
					if len(names) >= maxSheetNames {
						return names, errSheetNamesTruncated
					}
				}
			}

			stack = append(stack, typed.Name.Local)
		case xml.EndElement:
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		case xml.CharData:
			read += len(typed)
			if read > maxSheetNameBytes {
				return names, errSheetNamesTruncated
			}
		}
	}
}

func attrLocal(attrs []xml.Attr, local string) string {
	for _, attr := range attrs {
		if attr.Name.Local == local {
			return strings.TrimSpace(attr.Value)
		}
	}

	return ""
}
