// Copyright 2026 Paul Parks. SPDX-License-Identifier: Apache-2.0

// Package design edits an HTML document, such as an applet's markup, as a
// tree of elements, which is how PUDL Studio's canvas, its properties and
// an agent change it. The document stays plain HTML, with nothing of
// Studio's own written into it: an element is addressed by its path, the
// positions of the elements from the top down, such as "1.0.2", as they
// stand at one revision. Every change is made against the revision it was
// read at, so a path never points at the wrong element; a change against
// an older revision is refused, as a stale save is.
package design

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/andybalholm/cascadia"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

var (
	ErrPath     = errors.New("there is no element at that path")
	ErrPosition = errors.New("the position must be before, after, first or last")
	ErrName     = errors.New("that is not an attribute name")
	ErrInside   = errors.New("an element cannot be moved into itself")
	ErrMarkup   = errors.New("the markup to insert has no element")
)

// Doc is a document: the nodes of an HTML fragment, held under a body
// element of its own so that every element has a parent.
type Doc struct{ root *html.Node }

// Parse reads a fragment of HTML, as an applet's markup is, in the
// context of a body.
func Parse(src []byte) (*Doc, error) {
	body := &html.Node{Type: html.ElementNode, Data: "body", DataAtom: atom.Body}
	nodes, err := html.ParseFragment(bytes.NewReader(src), body)
	if err != nil {
		return nil, err
	}
	for _, n := range nodes {
		body.AppendChild(n)
	}
	return &Doc{root: body}, nil
}

// Render writes the document back as HTML, as a person writes it: void
// elements without a closing slash, attributes in double quotes, and only
// the characters that must be escaped escaped, so a file read and written
// with no change comes back as it was, entities written as characters
// aside.
func (d *Doc) Render() []byte {
	var b bytes.Buffer
	for c := d.root.FirstChild; c != nil; c = c.NextSibling {
		write(&b, c)
	}
	return b.Bytes()
}

var void = map[string]bool{"area": true, "base": true, "br": true, "col": true, "embed": true, "hr": true, "img": true,
	"input": true, "link": true, "meta": true, "source": true, "track": true, "wbr": true}

var rawText = map[string]bool{"script": true, "style": true}

var (
	textEscape = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", " ", "&nbsp;")
	attrEscape = strings.NewReplacer("&", "&amp;", `"`, "&quot;", " ", "&nbsp;")
)

func write(b *bytes.Buffer, n *html.Node) {
	switch n.Type {
	case html.TextNode:
		if n.Parent != nil && rawText[n.Parent.Data] {
			b.WriteString(n.Data)
		} else {
			b.WriteString(textEscape.Replace(n.Data))
		}
	case html.CommentNode:
		b.WriteString("<!--" + n.Data + "-->")
	case html.ElementNode:
		b.WriteString("<" + n.Data)
		for _, a := range n.Attr {
			b.WriteString(" " + a.Key)
			if a.Val != "" || !boolean[a.Key] {
				b.WriteString(`="` + attrEscape.Replace(a.Val) + `"`)
			}
		}
		b.WriteString(">")
		if void[n.Data] {
			return
		}
		// A leading newline in pre or textarea is dropped by the parser,
		// so one that was meant is written twice.
		if (n.Data == "pre" || n.Data == "textarea") && n.FirstChild != nil && n.FirstChild.Type == html.TextNode && strings.HasPrefix(n.FirstChild.Data, "\n") {
			b.WriteString("\n")
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			write(b, c)
		}
		b.WriteString("</" + n.Data + ">")
	}
}

// Attributes whose presence is their meaning are written without a value.
var boolean = map[string]bool{"checked": true, "disabled": true, "hidden": true, "required": true, "readonly": true,
	"selected": true, "multiple": true, "autofocus": true, "open": true, "popover": true, "inert": true, "novalidate": true,
	"defer": true, "async": true, "data-filter-menu": true, "data-seg-menu": true}

func elements(n *html.Node) []*html.Node {
	var out []*html.Node
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode {
			out = append(out, c)
		}
	}
	return out
}

// find returns the element at a path; the empty path is the document.
func (d *Doc) find(path string) (*html.Node, error) {
	n := d.root
	if path == "" {
		return n, nil
	}
	for _, part := range strings.Split(path, ".") {
		i, err := strconv.Atoi(part)
		els := elements(n)
		if err != nil || i < 0 || i >= len(els) {
			return nil, ErrPath
		}
		n = els[i]
	}
	return n, nil
}

// PathOf is an element's path.
func (d *Doc) PathOf(n *html.Node) string {
	var parts []string
	for ; n != nil && n != d.root; n = n.Parent {
		i := 0
		for c := n.Parent.FirstChild; c != n; c = c.NextSibling {
			if c.Type == html.ElementNode {
				i++
			}
		}
		parts = append([]string{strconv.Itoa(i)}, parts...)
	}
	return strings.Join(parts, ".")
}

// Node is an element as Studio lists it.
type Node struct {
	Path     string
	Tag      string
	Attrs    []html.Attribute
	Text     string // its own words, where it has no elements inside
	Children []Node
}

// Tree lists the document's elements, or those under the element at a
// path.
func (d *Doc) Tree(path string) ([]Node, error) {
	n, err := d.find(path)
	if err != nil {
		return nil, err
	}
	var out []Node
	for _, c := range elements(n) {
		out = append(out, d.node(c, true))
	}
	return out, nil
}

// Element is the element at a path, with its children listed but not
// theirs.
func (d *Doc) Element(path string) (Node, error) {
	n, err := d.find(path)
	if err != nil || n == d.root {
		return Node{}, ErrPath
	}
	nd := d.node(n, false)
	for _, c := range elements(n) {
		nd.Children = append(nd.Children, d.node(c, false))
	}
	return nd, nil
}

func (d *Doc) node(n *html.Node, deep bool) Node {
	nd := Node{Path: d.PathOf(n), Tag: n.Data, Attrs: n.Attr}
	if len(elements(n)) == 0 {
		var sb strings.Builder
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if c.Type == html.TextNode {
				sb.WriteString(c.Data)
			}
		}
		nd.Text = strings.Join(strings.Fields(sb.String()), " ")
	}
	if deep {
		for _, c := range elements(n) {
			nd.Children = append(nd.Children, d.node(c, true))
		}
	}
	return nd
}

// Find returns the path of the first element, in document order, that
// matches a CSS selector: the element at path itself, or one inside it.
// It returns the empty string and no error where none matches.
func (d *Doc) Find(path, selector string) (string, error) {
	n, err := d.find(path)
	if err != nil || n == d.root {
		return "", ErrPath
	}
	sel, err := cascadia.Compile(selector)
	if err != nil {
		return "", fmt.Errorf("%q is not a selector: %w", selector, err)
	}
	if sel.Match(n) {
		return path, nil
	}
	if m := sel.MatchFirst(n); m != nil {
		return d.PathOf(m), nil
	}
	return "", nil
}

// Matches says whether the element at path matches a CSS selector.
func (d *Doc) Matches(path, selector string) (bool, error) {
	n, err := d.find(path)
	if err != nil || n == d.root {
		return false, ErrPath
	}
	sel, err := cascadia.Compile(selector)
	if err != nil {
		return false, fmt.Errorf("%q is not a selector: %w", selector, err)
	}
	return sel.Match(n), nil
}

// Attr returns an attribute of the element at path, and whether it has it.
func (d *Doc) Attr(path, name string) (string, bool) {
	n, err := d.find(path)
	if err != nil || n == d.root {
		return "", false
	}
	for _, a := range n.Attr {
		if a.Namespace == "" && a.Key == strings.ToLower(name) {
			return a.Val, true
		}
	}
	return "", false
}

// HasClass says whether the element at path has a class.
func (d *Doc) HasClass(path, class string) bool {
	v, _ := d.Attr(path, "class")
	for _, c := range strings.Fields(v) {
		if c == class {
			return true
		}
	}
	return false
}

// SetClass adds a class to the element at path, or takes it away, keeping
// its other classes in their order. An element left with no class loses
// the attribute.
func (d *Doc) SetClass(path, class string, on bool) error {
	if strings.ContainsAny(class, " \t\n\f\r") || class == "" {
		return fmt.Errorf("%q is not a class name", class)
	}
	v, _ := d.Attr(path, "class")
	var kept []string
	for _, c := range strings.Fields(v) {
		if c != class {
			kept = append(kept, c)
		}
	}
	if on {
		kept = append(kept, class)
	}
	if len(kept) == 0 {
		return d.RemoveAttr(path, "class")
	}
	return d.SetAttr(path, "class", strings.Join(kept, " "))
}

var attrName = regexp.MustCompile(`^[a-zA-Z_:][-a-zA-Z0-9_:.]*$`)

// SetAttr sets an attribute of the element at a path, adding it if it is
// not there.
func (d *Doc) SetAttr(path, name, value string) error {
	n, err := d.find(path)
	if err != nil || n == d.root {
		return ErrPath
	}
	name = strings.ToLower(name)
	if !attrName.MatchString(name) {
		return ErrName
	}
	for i, a := range n.Attr {
		if a.Key == name && a.Namespace == "" {
			n.Attr[i].Val = value
			return nil
		}
	}
	n.Attr = append(n.Attr, html.Attribute{Key: name, Val: value})
	return nil
}

// RemoveAttr removes an attribute of the element at a path.
func (d *Doc) RemoveAttr(path, name string) error {
	n, err := d.find(path)
	if err != nil || n == d.root {
		return ErrPath
	}
	name = strings.ToLower(name)
	kept := n.Attr[:0]
	for _, a := range n.Attr {
		if a.Key != name {
			kept = append(kept, a)
		}
	}
	n.Attr = kept
	return nil
}

// SetText makes the words of the element at a path its whole content.
func (d *Doc) SetText(path, text string) error {
	n, err := d.find(path)
	if err != nil || n == d.root {
		return ErrPath
	}
	for c := n.FirstChild; c != nil; {
		next := c.NextSibling
		n.RemoveChild(c)
		c = next
	}
	n.AppendChild(&html.Node{Type: html.TextNode, Data: text})
	return nil
}

// Insert puts a fragment of markup, such as a component's template,
// before or after the element at a path, or first or last inside it; the
// empty path is the document. It returns the path of the first element
// inserted.
func (d *Doc) Insert(path, position string, markup []byte) (string, error) {
	n, err := d.find(path)
	if err != nil {
		return "", err
	}
	parent := n
	if position == "before" || position == "after" {
		if n == d.root {
			return "", ErrPosition
		}
		parent = n.Parent
	}
	nodes, err := html.ParseFragment(bytes.NewReader(markup), &html.Node{Type: html.ElementNode, Data: parent.Data, DataAtom: parent.DataAtom})
	if err != nil {
		return "", err
	}
	var first *html.Node
	for _, x := range nodes {
		if x.Type == html.ElementNode && first == nil {
			first = x
		}
	}
	if first == nil {
		return "", ErrMarkup
	}
	if err := place(n, position, nodes); err != nil {
		return "", err
	}
	return d.PathOf(first), nil
}

func place(n *html.Node, position string, nodes []*html.Node) error {
	switch position {
	case "before":
		for _, x := range nodes {
			n.Parent.InsertBefore(x, n)
		}
	case "after":
		at := n.NextSibling
		for _, x := range nodes {
			n.Parent.InsertBefore(x, at)
		}
	case "first":
		at := n.FirstChild
		for _, x := range nodes {
			n.InsertBefore(x, at)
		}
	case "last":
		for _, x := range nodes {
			n.AppendChild(x)
		}
	default:
		return ErrPosition
	}
	return nil
}

// Remove takes the element at a path out of the document.
func (d *Doc) Remove(path string) error {
	n, err := d.find(path)
	if err != nil || n == d.root {
		return ErrPath
	}
	n.Parent.RemoveChild(n)
	return nil
}

// Move takes the element at a path and puts it before or after another,
// or first or last inside it; it returns the element's new path.
func (d *Doc) Move(path, target, position string) (string, error) {
	n, err := d.find(path)
	if err != nil || n == d.root {
		return "", ErrPath
	}
	t, err := d.find(target)
	if err != nil {
		return "", err
	}
	for p := t; p != nil; p = p.Parent {
		if p == n {
			return "", ErrInside
		}
	}
	if (position == "before" || position == "after") && t == d.root {
		return "", ErrPosition
	}
	if position != "before" && position != "after" && position != "first" && position != "last" {
		return "", ErrPosition
	}
	n.Parent.RemoveChild(n)
	if err := place(t, position, []*html.Node{n}); err != nil {
		return "", err
	}
	return d.PathOf(n), nil
}

// Describe is an element as one line: its tag, id and classes.
func Describe(n Node) string {
	s := n.Tag
	for _, a := range n.Attrs {
		switch a.Key {
		case "id":
			s += "#" + a.Val
		case "class":
			for _, c := range strings.Fields(a.Val) {
				s += "." + c
			}
		}
	}
	return s
}

// ParsePath checks a path's form.
func ParsePath(p string) (string, error) {
	if p == "" {
		return "", nil
	}
	for _, part := range strings.Split(p, ".") {
		if _, err := strconv.Atoi(part); err != nil {
			return "", fmt.Errorf("%q is not a path", p)
		}
	}
	return p, nil
}
