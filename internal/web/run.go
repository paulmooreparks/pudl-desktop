// Copyright 2026 Paul Parks. SPDX-License-Identifier: Apache-2.0

package web

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"strconv"
	"strings"

	"github.com/paulmooreparks/pudl-desktop/internal/store"
)

// === The run origin ========================================================
//
// What is built in PUDL Studio runs on an origin of its own, apart from the
// desktop, so that code a reader or an agent writes can never reach a
// workspace. The run origin holds no workspace token and serves none of
// the desktop's pages. It serves a document's revision at a capability
// address, /v/{ws}/{file}/{rev}/{sig}, which the desktop signs for whoever
// holds the workspace: the address shows that one revision and nothing
// else, and a revision never changes.

func (s *Server) isRunHost(host string) bool {
	for _, h := range s.RunHosts {
		if h != "" && strings.EqualFold(host, h) {
			return true
		}
	}
	return false
}

func (s *Server) sign(ws, file string, rev int) string {
	m := hmac.New(sha256.New, s.previewKey)
	fmt.Fprintf(m, "%s/%s/%d", ws, file, rev)
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil)[:16])
}

func (s *Server) previewPath(f *store.Entry, rev int) string {
	return fmt.Sprintf("/v/%s/%s/%d/%s", f.Workspace, f.ID, rev, s.sign(f.Workspace, f.ID, rev))
}

func (s *Server) runRoutes() *http.ServeMux {
	mux := http.NewServeMux()
	static, _ := fs.Sub(staticFS, "static")
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(static)))
	mux.HandleFunc("GET /v/{ws}/{file}/{rev}/{sig}", s.preview)
	return mux
}

// preview serves a document's revision as a page of its own, with PUDL
// and nothing of the desktop's, under a policy that lets it load only
// what the run origin serves.
func (s *Server) preview(w http.ResponseWriter, r *http.Request) {
	ws, file, sig := r.PathValue("ws"), r.PathValue("file"), r.PathValue("sig")
	rev, err := strconv.Atoi(r.PathValue("rev"))
	if err != nil || !hmac.Equal([]byte(sig), []byte(s.sign(ws, file, rev))) {
		http.NotFound(w, r)
		return
	}
	f, err := s.store.Entry(r.Context(), ws, file)
	if err != nil || !isHTML(f) {
		http.NotFound(w, r)
		return
	}
	rv, err := s.store.Revision(r.Context(), f, rev)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	text, err := s.store.Read(f, rv)
	if err != nil {
		http.Error(w, "The document could not be read.", http.StatusInternalServerError)
		return
	}
	theme := "light"
	if r.URL.Query().Get("theme") == "dark" {
		theme = "dark"
	}
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self'; connect-src 'self'; base-uri 'none'; form-action 'none'")
	h.Set("Cache-Control", "private, max-age=3600")
	h.Set("Referrer-Policy", "no-referrer")
	s.pages.ExecuteTemplate(w, "preview", map[string]any{
		"Title": f.Name, "Theme": theme, "Doc": template.HTML(text),
	})
}

// === The canvas ============================================================

func (s *Server) canvasView(w http.ResponseWriter, r *http.Request, ws *store.Workspace) {
	f, err := s.entry(r, ws)
	if err != nil || !isHTML(f) {
		s.notFound(w, r)
		return
	}
	theme := "light"
	if r.URL.Query().Get("theme") == "dark" {
		theme = "dark"
	}
	data := s.fileLinks(r.Context(), f)
	data["Title"] = "Canvas of " + f.Name
	data["Key"] = "canvas-" + f.ID
	data["Theme"] = theme
	data["Other"] = map[string]string{"light": "dark", "dark": "light"}[theme]
	if s.RunOrigin != "" {
		data["Frame"] = s.RunOrigin + s.previewPath(f, f.Latest) + "?theme=" + theme
	}
	data["Picture"] = fmt.Sprintf("%s/canvas.png?width=1024&theme=%s", idURL(f), theme)
	data["PhonePicture"] = fmt.Sprintf("%s/canvas.png?width=390&theme=%s", idURL(f), theme)
	s.render(w, r, http.StatusOK, "canvas", data)
}

// picture draws the canvas in the service's own browser, for an agent to
// see what a person sees.
func (s *Server) picture(w http.ResponseWriter, r *http.Request, ws *store.Workspace) {
	f, err := s.entry(r, ws)
	if err != nil || !isHTML(f) {
		s.notFound(w, r)
		return
	}
	if s.Renderer == nil || s.RenderBase == "" {
		http.Error(w, "This desktop draws no pictures.", http.StatusServiceUnavailable)
		return
	}
	width, err := strconv.Atoi(r.URL.Query().Get("width"))
	if err != nil {
		width = 1024
	}
	width = min(max(width, 320), 1920)
	theme := "light"
	if r.URL.Query().Get("theme") == "dark" {
		theme = "dark"
	}
	png, err := s.Renderer.Picture(r.Context(), s.RenderBase+s.previewPath(f, f.Latest)+"?theme="+theme, width)
	if err != nil {
		http.Error(w, "The picture could not be drawn: "+err.Error(), http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(png)
}

// latest is a file's latest revision number, which an open window checks
// so that it follows changes made elsewhere, by an agent or in another
// window.
func (s *Server) latest(w http.ResponseWriter, r *http.Request, ws *store.Workspace) {
	f, err := s.entry(r, ws)
	if err != nil || f.Kind != store.File {
		s.notFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	fmt.Fprint(w, f.Latest)
}
