// Copyright 2026 Paul Parks. SPDX-License-Identifier: Apache-2.0

// Package hyper reads a PUDL Desktop page as a client that only follows
// links and fills forms sees it: its title, its words, and what it offers,
// the links with their relations and the forms with their fields. The
// terminal, the MCP server and the tests all read pages through it, so
// none of them knows anything about a page that the page does not say.
package hyper

import (
	"io"
	"net/url"
	"strings"

	"golang.org/x/net/html"
)

// Page is a page as links, forms and words.
type Page struct {
	URL   string `json:"url"`
	Title string `json:"title"`
	Text  string `json:"text"`
	Links []Link `json:"links"`
	Forms []Form `json:"forms"`
}

// Link is an a or link element with an address.
type Link struct {
	Rel  string `json:"rel,omitempty"`
	Href string `json:"href"`
	Type string `json:"type,omitempty"`
	Text string `json:"text,omitempty"`
}

// Form is a form, with the fields a client fills and the buttons that
// send it.
type Form struct {
	Rel     string   `json:"rel,omitempty"`
	Method  string   `json:"method"`
	Action  string   `json:"action"`
	Fields  []Field  `json:"fields"`
	Buttons []Button `json:"buttons,omitempty"`
}

// Field is one named control of a form, with its current value. Options
// lists a select's choices.
type Field struct {
	Name     string   `json:"name"`
	Type     string   `json:"type"`
	Value    string   `json:"value"`
	Label    string   `json:"label,omitempty"`
	Required bool     `json:"required,omitempty"`
	Checked  bool     `json:"checked,omitempty"`
	Options  []Option `json:"options,omitempty"`
}

type Option struct {
	Value    string `json:"value"`
	Text     string `json:"text"`
	Selected bool   `json:"selected,omitempty"`
}

// Button is a submit button; one with a name sends its value too.
type Button struct {
	Text  string `json:"text"`
	Name  string `json:"name,omitempty"`
	Value string `json:"value,omitempty"`
}

// Read parses a page fetched from base. Addresses are made absolute paths
// on the page's own site where they are on it, and left whole elsewhere.
func Read(r io.Reader, base string) (*Page, error) {
	doc, err := html.Parse(r)
	if err != nil {
		return nil, err
	}
	b, _ := url.Parse(base)
	p := &Page{URL: base}
	labels := map[string]string{}
	walk(doc, func(n *html.Node) {
		if n.Data == "label" {
			if f := attr(n, "for"); f != "" {
				labels[f] = text(n)
			}
		}
	})
	walk(doc, func(n *html.Node) {
		switch n.Data {
		case "title":
			if p.Title == "" {
				p.Title = text(n)
			}
		case "a", "link":
			// What a page loads to draw itself is not somewhere to go.
			switch strings.ToLower(attr(n, "rel")) {
			case "stylesheet", "icon", "preload", "modulepreload", "manifest":
				return
			}
			if href := attr(n, "href"); href != "" && attr(n, "data-menubar-fallback") == "" {
				p.Links = append(p.Links, Link{Rel: attr(n, "rel"), Href: resolve(b, href), Type: attr(n, "type"), Text: text(n)})
			}
		case "form":
			p.Forms = append(p.Forms, readForm(n, b, labels))
		}
	})
	p.Text = content(doc)
	return p, nil
}

func readForm(n *html.Node, b *url.URL, labels map[string]string) Form {
	method := strings.ToUpper(attr(n, "method"))
	if method == "" {
		method = "GET"
	}
	f := Form{Rel: attr(n, "rel"), Method: method, Action: resolve(b, attr(n, "action"))}
	if f.Action == "" {
		f.Action = b.Path
	}
	walk(n, func(c *html.Node) {
		switch c.Data {
		case "input":
			typ := strings.ToLower(attr(c, "type"))
			if typ == "" {
				typ = "text"
			}
			name := attr(c, "name")
			if typ == "submit" {
				f.Buttons = append(f.Buttons, Button{Text: attr(c, "value"), Name: name, Value: attr(c, "value")})
				return
			}
			if name == "" || typ == "button" || typ == "reset" || typ == "image" {
				return
			}
			fd := Field{Name: name, Type: typ, Value: attr(c, "value"), Required: has(c, "required"), Label: labels[attr(c, "id")]}
			if typ == "checkbox" || typ == "radio" {
				if fd.Value == "" {
					fd.Value = "on"
				}
				fd.Checked = has(c, "checked")
				if fd.Label == "" && c.Parent != nil && c.Parent.Data == "label" {
					fd.Label = text(c.Parent)
				}
			}
			f.Fields = append(f.Fields, fd)
		case "textarea":
			if name := attr(c, "name"); name != "" {
				var sb strings.Builder
				for ch := c.FirstChild; ch != nil; ch = ch.NextSibling {
					sb.WriteString(ch.Data)
				}
				f.Fields = append(f.Fields, Field{Name: name, Type: "textarea", Value: sb.String(), Required: has(c, "required"), Label: labels[attr(c, "id")]})
			}
		case "select":
			if name := attr(c, "name"); name != "" {
				fd := Field{Name: name, Type: "select", Required: has(c, "required"), Label: labels[attr(c, "id")]}
				walk(c, func(o *html.Node) {
					if o.Data != "option" {
						return
					}
					v := attr(o, "value")
					if !has(o, "value") {
						v = text(o)
					}
					sel := has(o, "selected")
					fd.Options = append(fd.Options, Option{Value: v, Text: text(o), Selected: sel})
					if sel || fd.Value == "" && len(fd.Options) == 1 {
						fd.Value = v
					}
				})
				f.Fields = append(f.Fields, fd)
			}
		case "button":
			if t := strings.ToLower(attr(c, "type")); t == "" || t == "submit" {
				f.Buttons = append(f.Buttons, Button{Text: text(c), Name: attr(c, "name"), Value: attr(c, "value")})
			}
		}
	})
	return f
}

// Values is a form's fields as they would be sent, with changes applied:
// a checkbox or radio button is sent only while checked.
func (f Form) Values(changes map[string]string) url.Values {
	v := url.Values{}
	for _, fd := range f.Fields {
		if (fd.Type == "checkbox" || fd.Type == "radio") && !fd.Checked {
			continue
		}
		v.Add(fd.Name, fd.Value)
	}
	for k, val := range changes {
		v.Set(k, val)
	}
	return v
}

// content is the words of what the page is about: a window's content, or
// a page's main, or its body, with blocks on lines of their own and
// scripts, styles and hidden lists left out.
func content(doc *html.Node) string {
	var root *html.Node
	walk(doc, func(n *html.Node) {
		if root == nil && (n.Data == "main" || hasClass(n, "win-content")) {
			root = n
		}
	})
	if root == nil {
		walk(doc, func(n *html.Node) {
			if root == nil && n.Data == "body" {
				root = n
			}
		})
	}
	if root == nil {
		return ""
	}
	var sb strings.Builder
	var emit func(n *html.Node)
	emit = func(n *html.Node) {
		if n.Type == html.TextNode {
			sb.WriteString(n.Data)
			return
		}
		if n.Type == html.ElementNode {
			switch n.Data {
			case "script", "style", "template", "select", "textarea":
				return
			}
			if has(n, "hidden") {
				return
			}
		}
		block := n.Type == html.ElementNode && isBlock(n.Data)
		if block {
			sb.WriteString("\n")
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			emit(c)
		}
		if block {
			sb.WriteString("\n")
		}
		// A table's cells are words apart, as they are on the screen.
		if n.Type == html.ElementNode && (n.Data == "th" || n.Data == "td") {
			sb.WriteString(" ")
		}
	}
	emit(root)
	var lines []string
	for _, l := range strings.Split(sb.String(), "\n") {
		if l = strings.Join(strings.Fields(l), " "); l != "" {
			lines = append(lines, l)
		}
	}
	return strings.Join(lines, "\n")
}

func isBlock(tag string) bool {
	switch tag {
	case "p", "div", "section", "article", "header", "footer", "main", "nav", "h1", "h2", "h3", "h4", "h5", "h6",
		"li", "tr", "table", "pre", "blockquote", "form", "ul", "ol", "label", "br", "hr", "dt", "dd":
		return true
	}
	return false
}

func walk(n *html.Node, f func(*html.Node)) {
	if n.Type == html.ElementNode {
		f(n)
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		walk(c, f)
	}
}

func attr(n *html.Node, name string) string {
	for _, a := range n.Attr {
		if a.Key == name {
			return a.Val
		}
	}
	return ""
}

func has(n *html.Node, name string) bool {
	for _, a := range n.Attr {
		if a.Key == name {
			return true
		}
	}
	return false
}

func hasClass(n *html.Node, class string) bool {
	for _, c := range strings.Fields(attr(n, "class")) {
		if c == class {
			return true
		}
	}
	return false
}

func text(n *html.Node) string {
	var sb strings.Builder
	var f func(*html.Node)
	f = func(n *html.Node) {
		if n.Type == html.TextNode {
			sb.WriteString(n.Data)
		}
		if n.Type == html.ElementNode && (n.Data == "script" || n.Data == "style") {
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			f(c)
		}
	}
	f(n)
	return strings.Join(strings.Fields(sb.String()), " ")
}

func resolve(b *url.URL, href string) string {
	if href == "" || b == nil {
		return href
	}
	u, err := b.Parse(href)
	if err != nil {
		return href
	}
	if u.Host == b.Host && u.Scheme == b.Scheme {
		u.Scheme, u.Host = "", ""
		u.User = nil
		return u.String()
	}
	return u.String()
}
