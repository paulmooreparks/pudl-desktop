// Copyright 2026 Paul Parks. SPDX-License-Identifier: Apache-2.0

package web

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chromedp/chromedp"
	"github.com/paulmooreparks/pudl-desktop/internal/store"
)

// An applets folder with a good package, Hello, and a package whose name
// is not its folder's, which the desktop leaves out.
func appletsFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	hello := filepath.Join(dir, "hello")
	must(t, os.MkdirAll(hello, 0o755))
	files := map[string]string{
		"applet.json": `{ "name": "hello", "title": "Hello", "version": "0.1.0", "description": "A greeting.", "src": "hello.js", "css": "hello.css", "icon": "icon.svg", "page": { "body": "page.html" }, "window": { "shape": "0.3,0.3", "min": "260,160" }, "tags": ["example"] }`,
		"hello.js": `(function () { window.pudlApplets.register('hello', { init: function (root, opts) {
			var host = window.pudlHost || {};
			root.innerHTML = '<p class="hello-greeting">Hello from a package.</p><p class="hello-host">' + (host.identity ? 'host' : 'no host') + ' ' + (host.pages || '') + '</p>';
			return { destroy: function () {}, menus: function () { return { titles: [{ label: 'Hello', items: host.identity ? host.identity(root, [{ label: 'Wave', run: function () {} }]) : [] }] }; } };
		} }); })();`,
		"hello.css": ".hello-greeting { font-weight: 700; }",
		"icon.svg":  `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 32 32"><rect width="32" height="32" fill="#4a90e2"/></svg>`,
		"page.html": `<div data-applet="hello" role="application" aria-label="Hello"><noscript><p>Hello needs JavaScript.</p></noscript></div>`,
	}
	for name, text := range files {
		must(t, os.WriteFile(filepath.Join(hello, name), []byte(text), 0o644))
	}
	wrong := filepath.Join(dir, "wrong")
	must(t, os.MkdirAll(wrong, 0o755))
	must(t, os.WriteFile(filepath.Join(wrong, "applet.json"), []byte(`{ "name": "other", "title": "Other", "version": "1", "src": "a.js" }`), 0o644))
	must(t, os.WriteFile(filepath.Join(wrong, "a.js"), []byte(""), 0o644))
	return dir
}

func newDesktopWithApplets(t *testing.T) *httptest.Server {
	st, err := store.Open(t.TempDir())
	must(t, err)
	t.Cleanup(func() { st.Close() })
	srv, err := New(st)
	must(t, err)
	srv.Applets = appletsFixture(t)
	hs := httptest.NewServer(srv)
	t.Cleanup(hs.Close)
	return hs
}

func get(t *testing.T, url string) (int, string, http.Header) {
	t.Helper()
	resp, err := http.Get(url)
	must(t, err)
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body), resp.Header
}

// TestAppletPackages: the registry, the page, the files and the menu a
// package gets, from its manifest alone, and the package the desktop
// refuses.
func TestAppletPackages(t *testing.T) {
	hs := newDesktopWithApplets(t)
	status, reg, h := get(t, hs.URL+"/applets/registry.js")
	if status != 200 || !strings.HasPrefix(h.Get("Content-Type"), "text/javascript") {
		t.Fatalf("registry: %d %s", status, h.Get("Content-Type"))
	}
	if !strings.Contains(reg, `pudlApplets.define("hello", {"css":"/applets/hello/hello.css","page":"/app/hello","src":"/applets/hello/hello.js","ver":"0.1.0"});`) {
		t.Errorf("registry defines hello from its manifest; got %q", reg)
	}
	if strings.Contains(reg, "other") || strings.Contains(reg, "wrong") {
		t.Errorf("a package whose name is not its folder's is left out; got %q", reg)
	}
	status, page, _ := get(t, hs.URL+"/app/hello")
	if status != 200 || !strings.Contains(page, `<div data-applet="hello"`) || !strings.Contains(page, "/static/pudl/pudl-applets.js") || !strings.Contains(page, "/static/pudl-host-kit.js") || !strings.Contains(page, "/applets/registry.js") {
		t.Errorf("the package's page holds its mount and the applet scripts; got %d %q", status, page)
	}
	if status, _, _ := get(t, hs.URL+"/app/nope"); status != 404 {
		t.Errorf("a package that is not installed has no page; got %d", status)
	}
	if status, body, _ := get(t, hs.URL+"/applets/hello/applet.json"); status != 200 || !strings.Contains(body, `"name": "hello"`) {
		t.Errorf("the package's files are served; got %d", status)
	}
	if status, _, _ := get(t, hs.URL+"/applets/hello/../../etc/passwd"); status == 200 {
		t.Errorf("a path out of the folder is refused")
	}
	kind, id, _ := parseKey("app-hello")
	if kind != "app" || id != "hello" || keyURL("x", "app-hello") != "/app/hello" {
		t.Errorf("app-hello is the package's window key; got %s %s %s", kind, id, keyURL("x", "app-hello"))
	}
	if kind, _, _ := parseKey("app-Hello!"); kind != "" {
		t.Errorf("a key that is not a package's name is no key")
	}
}

// TestAppletPackagesDesktop runs Hello on the desktop: in its window,
// from the Applets menu, with the host object the desktop gives it.
func TestAppletPackagesDesktop(t *testing.T) {
	hs := newDesktopWithApplets(t)
	ctx := browser(t)
	must(t, chromedp.Do(ctx,
		chromedp.Navigate(hs.URL+"/"),
		chromedp.Click(chromedp.CSS(`form[action="/workspaces"] button`)),
		chromedp.WaitVisible(chromedp.CSS(`.win[data-win^="r-"]`)),
	))
	url, err := js(ctx, `location.href`)
	must(t, err)
	must(t, chromedp.Do(ctx, chromedp.Navigate(url+"?open=app-hello&top=app-hello")))
	must(t, wait(ctx, `document.querySelector('.win[data-win="app-hello"] .hello-greeting')`))
	got, err := js(ctx, `document.querySelector('.win[data-win="app-hello"] .hello-host').textContent`)
	must(t, err)
	if got != "host /app/" {
		t.Errorf("the applet sees the desktop's host object; got %q", got)
	}
	minSize, err := js(ctx, `document.querySelector('.win[data-win="app-hello"]').getAttribute('data-win-min') || ''`)
	must(t, err)
	if minSize != "260,160" {
		t.Errorf("the window takes the manifest's least size; got %q", minSize)
	}
	menu, err := js(ctx, `(function () { var a = document.querySelector('[data-menubar-source] a[data-win-open="app-hello"]'); return a ? (a.textContent.trim() + ' ' + (a.querySelector('img') ? a.querySelector('img').getAttribute('src') : '')) : ''; })()`)
	must(t, err)
	if menu != "Hello /applets/hello/icon.svg" {
		t.Errorf("the Applets menu lists the package with its icon; got %q", menu)
	}
	must(t, chromedp.Do(ctx, chromedp.Click(chromedp.CSS(`.menubar-front .menubar-title`))))
	must(t, wait(ctx, `document.querySelector('#menubar-panel')`))
	front, err := js(ctx, `document.querySelector('#menubar-panel').innerText`)
	must(t, err)
	if !strings.Contains(front, "Wave") || !strings.Contains(front, "Open as a page") {
		t.Errorf("the applet's menu has its own entry and the window's commands; got %q", front)
	}
}
