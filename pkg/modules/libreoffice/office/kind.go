package office

// docKind selects the HTML post-processing a format needs.
type docKind int

const (
	kindText docKind = iota
	kindSheet
	kindSlides
)

func documentKind(ext string) docKind {
	switch ext {
	case ".xlsx", ".xls", ".ods":
		return kindSheet
	case ".pptx", ".ppt", ".odp":
		return kindSlides
	default:
		return kindText
	}
}
