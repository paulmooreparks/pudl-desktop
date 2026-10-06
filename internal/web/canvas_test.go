// Copyright 2026 Paul Parks. SPDX-License-Identifier: Apache-2.0

package web

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/paulmooreparks/pudl-desktop/internal/render"
	"github.com/paulmooreparks/pudl-desktop/internal/store"
)

// newDesktopAndRun serves one desktop on two origins, as the service does
// behind its tunnel: the desktop on one host and the run origin on
// another. The run origin's browser address and the address the service's
// own browser uses are the same here.
func newDesktopAndRun(t *testing.T) (desk, run *httptest.Server, srv *Server) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	srv, err = New(st)
	if err != nil {
		t.Fatal(err)
	}
	desk = httptest.NewServer(srv)
	run = httptest.NewServer(srv)
	t.Cleanup(desk.Close)
	t.Cleanup(run.Close)
	u, _ := url.Parse(run.URL)
	srv.RunHosts = []string{u.Host}
	srv.RunOrigin = run.URL
	srv.RenderBase = run.URL
	return desk, run, srv
}

// TestCanvas: an agent makes an applet and opens its canvas, which shows
// the document running on the run origin; the run origin serves only a
// signed revision, under its policy, and none of the desktop's pages.
func TestCanvas(t *testing.T) {
	desk, run, _ := newDesktopAndRun(t)
	a := newAgent(t, desk.URL)
	home := a.send(a.open("/"), RelBase+"start", nil)
	file := a.send(home, RelBase+"create", map[string]string{"name": "applet.html", "kind": "file"})
	design := a.open(a.link(file, RelBase+"design"))
	a.send(design, RelBase+"insert", map[string]string{"component": "settings-panel", "position": "last"})

	canvas := a.open(a.link(file, RelBase+"canvas"))
	if canvas.Structured.Status != http.StatusOK {
		t.Fatalf("the canvas should open:\n%s", canvas.Content[0].Text)
	}
	frame := a.link(canvas, "preview")
	if !strings.HasPrefix(frame, run.URL+"/v/") {
		t.Fatalf("the canvas should run the document on the run origin, got %q", frame)
	}

	resp, err := http.Get(frame)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `class="settings-panel"`) {
		t.Fatalf("the run origin should serve the document: %d\n%s", resp.StatusCode, body)
	}
	csp := resp.Header.Get("Content-Security-Policy")
	for _, s := range []string{"default-src 'none'", "script-src 'self'", "form-action 'none'"} {
		if !strings.Contains(csp, s) {
			t.Fatalf("the run origin's policy should have %s: %q", s, csp)
		}
	}

	// A signature for another revision, or none, opens nothing.
	parts := strings.Split(strings.TrimPrefix(frame, run.URL), "/")
	parts[4] = "1"
	for _, bad := range []string{strings.Join(parts, "/"), strings.TrimSuffix(strings.Split(frame, "?")[0], parts[5]) + "AAAA"} {
		if !strings.HasPrefix(bad, "http") {
			bad = run.URL + bad
		}
		r, err := http.Get(bad)
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		if r.StatusCode != http.StatusNotFound {
			t.Fatalf("%s should be refused, got %d", bad, r.StatusCode)
		}
	}

	// The run origin has none of the desktop's pages, nor its agent.
	for _, p := range []string{"/", "/mcp", "/w/" + strings.Split(strings.TrimPrefix(frame, run.URL+"/v/"), "/")[0] + "/"} {
		r, err := http.Get(run.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		if r.StatusCode != http.StatusNotFound && r.StatusCode != http.StatusMethodNotAllowed {
			t.Fatalf("the run origin should not serve %s, got %d", p, r.StatusCode)
		}
	}

	// The file's latest revision is there for a window to watch.
	latest := strings.TrimSuffix(a.link(file, RelBase+"canvas"), "/canvas") + "/latest"
	if got := a.open(latest); !strings.Contains(got.Content[0].Text, "2") {
		t.Fatalf("latest should give revision 2:\n%s", got.Content[0].Text)
	}
}

// TestPicture: the agent sees the canvas as a picture, drawn by the
// service's own browser. Skipped where there is no browser.
func TestPicture(t *testing.T) {
	path := os.Getenv("PUDL_CHROME")
	if path == "" {
		path = render.Find()
	}
	if path == "" {
		t.Skip("no Chromium browser to draw with; set PUDL_CHROME to one")
	}
	desk, _, srv := newDesktopAndRun(t)
	srv.Renderer = &render.Renderer{Path: path}
	t.Cleanup(srv.Renderer.Close)

	a := newAgent(t, desk.URL)
	home := a.send(a.open("/"), RelBase+"start", nil)
	file := a.send(home, RelBase+"create", map[string]string{"name": "applet.html", "kind": "file"})
	design := a.open(a.link(file, RelBase+"design"))
	a.send(design, RelBase+"insert", map[string]string{"component": "settings-panel", "position": "last"})
	canvas := a.open(a.link(file, RelBase+"canvas"))

	var raw json.RawMessage = a.rpc("tools/call", map[string]any{"name": "open", "arguments": map[string]any{"url": a.link(canvas, RelBase+"picture")}})
	var r struct {
		Content []struct {
			Type, MimeType, Data, Text string
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		t.Fatal(err)
	}
	if r.IsError || len(r.Content) == 0 || r.Content[0].Type != "image" || r.Content[0].MimeType != "image/png" {
		t.Fatalf("the picture should come to the agent as a PNG image: %s", raw[:min(len(raw), 400)])
	}
	png, err := base64.StdEncoding.DecodeString(r.Content[0].Data)
	if err != nil || !bytes.HasPrefix(png, []byte("\x89PNG\r\n\x1a\n")) || len(png) < 1000 {
		t.Fatalf("the picture should be a PNG, got %d bytes, %v", len(png), err)
	}
	// PUDL_PICTURE_OUT keeps the picture, for a person to look at.
	if out := os.Getenv("PUDL_PICTURE_OUT"); out != "" {
		os.WriteFile(out, png, 0o644)
	}
}
