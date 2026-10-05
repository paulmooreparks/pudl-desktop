// Copyright 2026 Paul Parks. SPDX-License-Identifier: Apache-2.0

package web

import (
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"golang.org/x/net/html"

	"github.com/paulmooreparks/pudl-desktop/internal/store"
)

// client follows the desktop's hypermedia the way the terminal will: it
// knows relation names and field names, and finds every address in the
// pages it is given. It never builds an address itself.
type client struct {
	t    *testing.T
	base string
	http *http.Client
}

func newClient(t *testing.T, base string) *client {
	jar, _ := cookiejar.New(nil)
	return &client{t: t, base: base, http: &http.Client{Jar: jar}}
}

type page struct {
	status int
	url    string
	body   string
	doc    *html.Node
}

func (c *client) get(u string) *page {
	c.t.Helper()
	resp, err := c.http.Get(c.abs(u))
	if err != nil {
		c.t.Fatal(err)
	}
	return c.read(resp)
}

func (c *client) abs(u string) string {
	if strings.HasPrefix(u, "http") {
		return u
	}
	return c.base + u
}

func (c *client) read(resp *http.Response) *page {
	c.t.Helper()
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	p := &page{status: resp.StatusCode, url: resp.Request.URL.String(), body: string(b)}
	if strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html") {
		doc, err := html.Parse(strings.NewReader(p.body))
		if err != nil {
			c.t.Fatal(err)
		}
		p.doc = doc
	}
	return p
}

func attr(n *html.Node, name string) string {
	for _, a := range n.Attr {
		if a.Key == name {
			return a.Val
		}
	}
	return ""
}

func find(n *html.Node, match func(*html.Node) bool) *html.Node {
	if n.Type == html.ElementNode && match(n) {
		return n
	}
	for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
		if f := find(ch, match); f != nil {
			return f
		}
	}
	return nil
}

func hasRel(n *html.Node, rel string) bool {
	for _, r := range strings.Fields(attr(n, "rel")) {
		if r == rel {
			return true
		}
	}
	return false
}

// link returns the address of the page's first link with the relation,
// and with the type if one is given.
func (p *page) link(t *testing.T, rel, typ string) string {
	t.Helper()
	n := find(p.doc, func(n *html.Node) bool {
		return (n.Data == "a" || n.Data == "link") && hasRel(n, rel) && (typ == "" || attr(n, "type") == typ)
	})
	if n == nil {
		t.Fatalf("%s has no link rel=%q type=%q", p.url, rel, typ)
	}
	return attr(n, "href")
}

// submit fills a form found by its relation and sends it. Hidden fields
// are sent as the page gave them; fields set the rest.
func (c *client) submit(p *page, rel string, fields map[string]string) *page {
	c.t.Helper()
	form := find(p.doc, func(n *html.Node) bool { return n.Data == "form" && hasRel(n, rel) })
	if form == nil {
		c.t.Fatalf("%s has no form rel=%q", p.url, rel)
	}
	vals := c.formValues(form)
	for k, v := range fields {
		vals.Set(k, v)
	}
	return c.post(attr(form, "action"), vals)
}

func (c *client) formValues(form *html.Node) url.Values {
	vals := url.Values{}
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch n.Data {
			case "input":
				if name := attr(n, "name"); name != "" {
					vals.Set(name, attr(n, "value"))
				}
			case "textarea":
				var sb strings.Builder
				for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
					sb.WriteString(ch.Data)
				}
				vals.Set(attr(n, "name"), sb.String())
			}
		}
		for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
			walk(ch)
		}
	}
	walk(form)
	return vals
}

func (c *client) post(action string, vals url.Values) *page {
	c.t.Helper()
	resp, err := c.http.PostForm(c.abs(action), vals)
	if err != nil {
		c.t.Fatal(err)
	}
	return c.read(resp)
}

func newDesktop(t *testing.T) *httptest.Server {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	srv, err := New(st)
	if err != nil {
		t.Fatal(err)
	}
	hs := httptest.NewServer(srv)
	t.Cleanup(hs.Close)
	return hs
}

func want(t *testing.T, p *page, status int) {
	t.Helper()
	if p.status != status {
		t.Fatalf("%s: status %d, want %d\n%s", p.url, p.status, status, p.body)
	}
}

// TestWalkthrough follows docs/walkthrough.md as far as stage 1's first
// slice reaches: a workspace, a folder, a Markdown file written in two
// revisions, its source and its rendered form, a conflict, and a form
// sent twice.
func TestWalkthrough(t *testing.T) {
	hs := newDesktop(t)
	c := newClient(t, hs.URL)

	// Starting a workspace lands on its root folder.
	root := c.submit(c.get("/"), RelBase+"start", nil)
	want(t, root, http.StatusOK)

	// Creating a folder, and in it a document.
	notes := c.submit(root, RelBase+"create", map[string]string{"name": "notes", "kind": "folder"})
	want(t, notes, http.StatusOK)
	file := c.submit(notes, RelBase+"create", map[string]string{"name": "report.md", "kind": "file"})
	want(t, file, http.StatusOK)
	if !strings.Contains(file.url, "/r/") {
		t.Fatalf("a new file should be shown at its identity address, got %s", file.url)
	}

	// A second entry of the same name is refused, with the form kept. The
	// folder is fetched again for a fresh form: sending the old one again
	// would replay the first creation, which is the point of its token.
	dup := c.submit(c.get(notes.url), RelBase+"create", map[string]string{"name": "report.md", "kind": "file"})
	want(t, dup, http.StatusUnprocessableEntity)
	if !strings.Contains(dup.body, "already exists") {
		t.Fatalf("the refusal should say why:\n%s", dup.body)
	}

	// Writing it.
	text := "# Report\n\n| Item | Cost |\n|---|---|\n| Hotel | 21600 |\n\n- [x] Book the hotel\n- [ ] Check the totals\n"
	edit := c.get(file.link(t, "edit-form", ""))
	saved := c.submit(edit, RelBase+"save", map[string]string{"text": text})
	want(t, saved, http.StatusOK)

	// Its source and its rendered form.
	raw := c.get(saved.link(t, "alternate", "text/markdown"))
	if raw.body != text {
		t.Fatalf("the source should be what was saved:\n%q", raw.body)
	}
	read := c.get(saved.link(t, "alternate", "text/html"))
	for _, s := range []string{`<h1 id="report">Report</h1>`, `<table class="data-table">`, `type="checkbox"`} {
		if !strings.Contains(read.body, s) {
			t.Fatalf("the rendered document should contain %s:\n%s", s, read.body)
		}
	}

	// The path address, which a person may type, reaches the same file and
	// gives its identity address as canonical. The Classic view is the
	// root folder at its path.
	classic := c.get(root.link(t, RelBase+"classic", ""))
	if !strings.HasSuffix(classic.url, "/files/") {
		t.Fatalf("the Classic view should open at the root folder's path, got %s", classic.url)
	}
	byPath := c.get(classic.url + "notes/report.md")
	want(t, byPath, http.StatusOK)
	if got := byPath.link(t, "canonical", ""); !strings.HasSuffix(file.url, got) {
		t.Fatalf("the path address should give %s as canonical, got %s", file.url, got)
	}

	// A conflict: two edits from revision 2. The first saves revision 3;
	// the second is refused with 409, and its text survives in the form
	// that saves against revision 3.
	first := c.get(saved.link(t, "edit-form", ""))
	second := c.get(saved.link(t, "edit-form", ""))
	want(t, c.submit(first, RelBase+"save", map[string]string{"text": text + "- [ ] Add the receipts\n"}), http.StatusOK)
	conflict := c.submit(second, RelBase+"save", map[string]string{"text": text + "Mine.\n"})
	want(t, conflict, http.StatusConflict)
	if !strings.Contains(conflict.body, "Mine.") || !strings.Contains(conflict.body, `name="base" value="3"`) {
		t.Fatalf("the conflict page should keep the text and offer a save against revision 3:\n%s", conflict.body)
	}
	resolved := c.submit(conflict, RelBase+"save", nil)
	want(t, resolved, http.StatusOK)
	if !strings.Contains(c.get(resolved.link(t, "alternate", "text/markdown")).body, "Mine.") {
		t.Fatal("saving from the conflict page should make revision 4 from the writer's text")
	}

	// A form sent twice makes one change.
	again := c.get(resolved.link(t, "edit-form", ""))
	form := find(again.doc, func(n *html.Node) bool { return n.Data == "form" && hasRel(n, RelBase+"save") })
	vals := c.formValues(form)
	vals.Set("text", "Once.\n")
	c.post(attr(form, "action"), vals)
	twice := c.post(attr(form, "action"), vals)
	want(t, twice, http.StatusOK)
	history := c.get(twice.link(t, "version-history", ""))
	if strings.Count(history.body, `rel="item"`) != 5 {
		t.Fatalf("a form sent twice should make one revision, five in all:\n%s", history.body)
	}
}

// TestViews follows the view switch: the workspace opens Windowed, on a
// desktop holding the root folder's window; choosing Classic is
// remembered, so the workspace's address then opens the root folder's
// page; choosing Windowed again goes back to the desktop.
func TestViews(t *testing.T) {
	hs := newDesktop(t)
	c := newClient(t, hs.URL)
	desk := c.submit(c.get("/"), RelBase+"start", nil)
	want(t, desk, http.StatusOK)
	win := find(desk.doc, func(n *html.Node) bool { return n.Data == "section" && strings.HasPrefix(attr(n, "data-win"), "r-") })
	if win == nil || !strings.Contains(desk.body, "data-win-src=") {
		t.Fatalf("the Windowed view should hold the root folder's window:\n%.800s", desk.body)
	}
	classic := c.get(desk.link(t, RelBase+"classic", ""))
	if !strings.HasSuffix(classic.url, "/files/") || strings.Contains(classic.body, "data-win-layer") {
		t.Fatalf("Classic should be the root folder's page, got %s", classic.url)
	}
	again := c.get(strings.TrimSuffix(classic.url, "files/"))
	if !strings.HasSuffix(again.url, "/files/") {
		t.Fatalf("the choice of Classic should be remembered, got %s", again.url)
	}
	back := c.get(classic.link(t, RelBase+"windowed", ""))
	if !strings.Contains(back.body, "data-win-layer") {
		t.Fatalf("Windowed should go back to the desktop, got %s", back.url)
	}
	// A window is the same resource as its page, in another representation,
	// at the address the layer's data-win-src gives for its key.
	layer := find(desk.doc, func(n *html.Node) bool { return n.Data == "div" && attr(n, "data-win-src") != "" })
	page := c.get(strings.ReplaceAll(attr(layer, "data-win-src"), "{key}", attr(win, "data-win")))
	if page.status != http.StatusOK || !strings.HasPrefix(strings.TrimSpace(page.body), "<section class=\"win\"") {
		t.Fatalf("a window's address should serve its window:\n%.400s", page.body)
	}
}

func TestWorkspaceBelongsToItsBrowser(t *testing.T) {
	hs := newDesktop(t)
	owner := newClient(t, hs.URL)
	root := owner.submit(owner.get("/"), RelBase+"start", nil)
	want(t, root, http.StatusOK)

	stranger := newClient(t, hs.URL)
	want(t, stranger.get(root.url), http.StatusNotFound)
}

func TestChangesMustComeFromTheDesktop(t *testing.T) {
	hs := newDesktop(t)
	c := newClient(t, hs.URL)
	root := c.submit(c.get("/"), RelBase+"start", nil)
	form := find(root.doc, func(n *html.Node) bool { return n.Data == "form" && hasRel(n, RelBase+"create") })
	vals := c.formValues(form)
	vals.Set("name", "x.md")
	req, _ := http.NewRequest(http.MethodPost, c.abs(attr(form, "action")), strings.NewReader(vals.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "https://elsewhere.example")
	resp, err := c.http.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("a POST from another origin should be refused, got %d", resp.StatusCode)
	}
}

func TestQuota(t *testing.T) {
	hs := newDesktop(t)
	c := newClient(t, hs.URL)
	root := c.submit(c.get("/"), RelBase+"start", nil)
	file := c.submit(root, RelBase+"create", map[string]string{"name": "big.md", "kind": "file"})
	edit := c.get(file.link(t, "edit-form", ""))
	big := strings.Repeat("x", store.Quota+1)
	refused := c.submit(edit, RelBase+"save", map[string]string{"text": big})
	want(t, refused, http.StatusUnprocessableEntity)
	if !strings.Contains(refused.body, "5 MB") {
		t.Fatalf("the refusal should state the limit:\n%.500s", refused.body)
	}

	// A form larger than any workspace could hold is refused before it is
	// read, and changes nothing.
	edit = c.get(file.link(t, "edit-form", ""))
	huge := c.submit(edit, RelBase+"save", map[string]string{"text": strings.Repeat("é", store.Quota)})
	want(t, huge, http.StatusRequestEntityTooLarge)
}
