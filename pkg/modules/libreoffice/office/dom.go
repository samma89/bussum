package office

import (
	"bytes"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

func parseHTML(raw string) (*html.Node, error) {
	doc, err := html.Parse(strings.NewReader(raw))
	if err != nil {
		return nil, err
	}

	return doc, nil
}

func findFirst(n *html.Node, name string) *html.Node {
	if n == nil {
		return nil
	}

	if n.Type == html.ElementNode && n.Data == name {
		return n
	}

	for child := n.FirstChild; child != nil; child = child.NextSibling {
		if found := findFirst(child, name); found != nil {
			return found
		}
	}

	return nil
}

func collect(n *html.Node, name string, out *[]*html.Node) {
	if n == nil {
		return
	}

	if n.Type == html.ElementNode && n.Data == name {
		*out = append(*out, n)
	}

	for child := n.FirstChild; child != nil; child = child.NextSibling {
		collect(child, name, out)
	}
}

func textContent(n *html.Node) string {
	if n == nil {
		return ""
	}

	var buf strings.Builder

	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.TextNode {
			buf.WriteString(node.Data)
		}

		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(n)

	return buf.String()
}

func plainText(n *html.Node) string {
	return strings.Join(strings.Fields(textContent(n)), " ")
}

func innerHTML(n *html.Node) (string, error) {
	if n == nil {
		return "", nil
	}

	var buf bytes.Buffer

	for child := n.FirstChild; child != nil; child = child.NextSibling {
		err := html.Render(&buf, child)
		if err != nil {
			return "", err
		}
	}

	return buf.String(), nil
}

func attr(n *html.Node, key string) string {
	for _, attribute := range n.Attr {
		if strings.EqualFold(attribute.Key, key) {
			return attribute.Val
		}
	}

	return ""
}

func hasAttr(n *html.Node, key string) bool {
	for _, attribute := range n.Attr {
		if strings.EqualFold(attribute.Key, key) {
			return true
		}
	}

	return false
}

func setAttr(n *html.Node, key, val string) {
	for i, attribute := range n.Attr {
		if strings.EqualFold(attribute.Key, key) {
			n.Attr[i].Val = val
			return
		}
	}

	n.Attr = append(n.Attr, html.Attribute{Key: key, Val: val})
}

func rename(n *html.Node, kind atom.Atom, data string) {
	n.Data = data
	n.DataAtom = kind
}

func unwrap(n *html.Node) {
	parent := n.Parent
	if parent == nil {
		return
	}

	for n.FirstChild != nil {
		child := n.FirstChild
		n.RemoveChild(child)
		parent.InsertBefore(child, n)
	}

	parent.RemoveChild(n)
}

// firstElement returns the first element child.
// A non-whitespace text node before that element means there is no leading element.
func firstElement(n *html.Node) *html.Node {
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		switch child.Type {
		case html.TextNode:
			if strings.TrimSpace(child.Data) != "" {
				return nil
			}
		case html.ElementNode:
			return child
		case html.ErrorNode, html.DocumentNode, html.CommentNode, html.DoctypeNode, html.RawNode:
		}
	}

	return nil
}

func firstText(n *html.Node) *html.Node {
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		if child.Type == html.TextNode {
			return child
		}
	}

	return nil
}

func isHeading(n *html.Node) bool {
	if n == nil || n.Type != html.ElementNode {
		return false
	}

	switch n.Data {
	case "h1", "h2", "h3", "h4", "h5", "h6":
		return true
	default:
		return false
	}
}
