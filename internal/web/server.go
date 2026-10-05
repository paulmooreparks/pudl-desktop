// Copyright 2026 Paul Parks. SPDX-License-Identifier: Apache-2.0

// Package web serves PUDL Desktop's resources as HTML, the one
// representation format of stage 1 (docs/walkthrough.md). Every page
// offers what can be done next as links and forms, each with a rel value,
// and leaves out what cannot be done. Every change is a POST to a form's
// action, which returns 303 See Other and the address of its result, so a
// reload or Back never sends it again.
package web

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/paulmooreparks/pudl-desktop/internal/markdown"
	"github.com/paulmooreparks/pudl-desktop/internal/mcp"
	"github.com/paulmooreparks/pudl-desktop/internal/store"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static
var staticFS embed.FS

// RelBase is where the desktop's own link relations are documented. Each
// relation the IANA registry lacks is an absolute URI under it (RFC 8288).
const RelBase = "https://pudl.parkscomputing.com/rel/"

// The desktop's own relations, and what each page at RelBase says of it.
var relations = map[string]string{
	"create":           "A form on a folder that creates a file or a folder in it. Its fields are name and kind (file or folder).",
	"save":             "A form that makes a new revision of a file from the text field. Its base field is the revision the text was written against; if the file has moved on since, the change is refused with 409 Conflict and nothing is lost.",
	"start":            "A form that starts a temporary workspace, which belongs to the browser that started it.",
	"history":          "The list of a file's revisions, newest first.",
	"windowed":         "A workspace's Windowed view, the desktop, whose windows show its resources; following it makes the Windowed view this browser's choice.",
	"classic":          "A workspace's Classic view, its resources as pages of their own; following it makes the Classic view this browser's choice.",
	"design":           "An HTML document's design: its elements as a tree, each with its path at the revision read, and the operations that change them.",
	"insert":           "A form that inserts a component from the palette, or markup, before or after the element at path, or first or last inside it. Its fields are path, position (before, after, first or last), component or markup, and base, the revision the path was read at.",
	"set-attribute":    "A form that sets an attribute of the element at path. Its fields are path, name, value and base.",
	"remove-attribute": "A form that removes an attribute of the element at path. Its fields are path, name and base.",
	"set-text":         "A form that makes the words of the element at path its whole content. Its fields are path, text and base.",
	"move":             "A form that moves the element at path before or after the element at target, or first or last inside it. Its fields are path, target, position and base.",
	"remove":           "A form that removes the element at path. Its fields are path and base.",
}

// Server serves the desktop.
type Server struct {
	store *store.Store
	pages *template.Template
	mux   *http.ServeMux
	// Secure marks the ownership cookie Secure; true behind HTTPS.
	Secure bool
}

func New(st *store.Store) (*Server, error) {
	funcs := template.FuncMap{
		"rel":  func(name string) string { return RelBase + name },
		"when": func(t time.Time) string { return t.UTC().Format("2006-01-02 15:04 UTC") },
		"size": humanSize,
		// wopen gives a link in a window the key of the window it opens.
		"wopen": func(data map[string]any, key string) template.HTMLAttr {
			if win, _ := data["Win"].(bool); !win || key == "" {
				return ""
			}
			return template.HTMLAttr(` data-win-open="` + template.HTMLEscapeString(key) + `"`)
		},
		"revkey": func(data map[string]any, n int) string {
			f, _ := data["File"].(*store.Entry)
			if f == nil {
				return ""
			}
			return fmt.Sprintf("rev-%s-%d", f.ID, n)
		},
	}
	pages, err := template.New("").Funcs(funcs).ParseFS(templateFS, "templates/*.html")
	if err != nil {
		return nil, err
	}
	s := &Server{store: st, pages: pages, mux: http.NewServeMux()}
	static, _ := fs.Sub(staticFS, "static")
	s.mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(static)))
	s.mux.HandleFunc("GET /{$}", s.welcome)
	s.mux.HandleFunc("POST /workspaces", s.startWorkspace)
	s.mux.HandleFunc("GET /rel/{name}", s.relation)
	s.mux.Handle("/mcp", mcp.New(s))
	s.mux.HandleFunc("GET /w/{ws}/agent", s.owned(s.agent))
	s.mux.HandleFunc("GET /w/{ws}/palette", s.owned(s.paletteView))
	s.mux.HandleFunc("GET /w/{ws}/r/{id}/design", s.owned(s.designView))
	s.mux.HandleFunc("GET /w/{ws}/r/{id}/design/{path}", s.owned(s.nodeView))
	s.mux.HandleFunc("POST /w/{ws}/r/{id}/design/ops/{op}", s.owned(s.operate))
	s.mux.HandleFunc("GET /w/{ws}/{$}", s.owned(s.workspaceRoot))
	s.mux.HandleFunc("GET /w/{ws}/win/{key}", s.owned(s.window))
	s.mux.HandleFunc("GET /w/{ws}/files/{path...}", s.owned(s.byPath))
	s.mux.HandleFunc("GET /w/{ws}/r/{id}", s.owned(s.byID))
	s.mux.HandleFunc("POST /w/{ws}/r/{id}/entries", s.owned(s.create))
	s.mux.HandleFunc("GET /w/{ws}/r/{id}/edit", s.owned(s.editForm))
	s.mux.HandleFunc("POST /w/{ws}/r/{id}/revisions", s.owned(s.save))
	s.mux.HandleFunc("GET /w/{ws}/r/{id}/revisions", s.owned(s.history))
	s.mux.HandleFunc("GET /w/{ws}/r/{id}/revisions/{n}", s.owned(s.revision))
	s.mux.HandleFunc("GET /w/{ws}/r/{id}/raw", s.owned(s.raw))
	s.mux.HandleFunc("GET /w/{ws}/r/{id}/rendered", s.owned(s.rendered))
	return s, nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// A change must come from a page of this desktop. The ownership cookie
	// is SameSite=Lax, so another site's form cannot send it, and a POST
	// whose Origin is another host is refused as well.
	if r.Method == http.MethodPost {
		if o := r.Header.Get("Origin"); o != "" {
			if u, err := url.Parse(o); err != nil || u.Host != r.Host {
				http.Error(w, "A change must come from this desktop's own pages.", http.StatusForbidden)
				return
			}
		}
		// A form is read whole before any handler sees it, so one too large
		// is refused outright rather than read in part. A textarea's text is
		// percent-encoded, up to three characters for each byte of a full
		// quota, and the form's other fields are small.
		r.Body = http.MaxBytesReader(w, r.Body, 3*store.Quota+64<<10)
		if err := r.ParseForm(); err != nil {
			var tooBig *http.MaxBytesError
			if errors.As(err, &tooBig) {
				s.render(w, r, http.StatusRequestEntityTooLarge, "problem", map[string]any{
					"Title":   "Too large",
					"Message": "That was more than a workspace can hold. Nothing was changed.",
				})
				return
			}
			http.Error(w, "The form could not be read.", http.StatusBadRequest)
			return
		}
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "same-origin")
	s.mux.ServeHTTP(w, r)
}

// === Workspaces ============================================================

func cookieName(ws string) string { return "ws-" + ws }

func (s *Server) welcome(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, http.StatusOK, "welcome", map[string]any{
		"Title":      "PUDL Desktop",
		"Submission": store.NewID(16),
		"Lifetime":   "24 hours from its last use",
		"Quota":      humanSize(store.Quota),
	})
}

func (s *Server) startWorkspace(w http.ResponseWriter, r *http.Request) {
	ws, token, err := s.store.CreateWorkspace(r.Context())
	if errors.Is(err, store.ErrFull) {
		w.Header().Set("Retry-After", "3600")
		s.render(w, r, http.StatusServiceUnavailable, "problem", map[string]any{
			"Title":   "The desktop is full",
			"Message": "The desktop has as many temporary workspaces as it can hold. Each one is deleted a day after its last use, so please try again later.",
		})
		return
	} else if err != nil {
		s.fail(w, r, err)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: cookieName(ws.ID), Value: token, Path: "/w/" + ws.ID + "/",
		HttpOnly: true, Secure: s.Secure, SameSite: http.SameSiteLaxMode,
		Expires: ws.Expires().Add(time.Hour),
	})
	http.Redirect(w, r, "/w/"+ws.ID+"/", http.StatusSeeOther)
}

type ownedHandler func(w http.ResponseWriter, r *http.Request, ws *store.Workspace)

// owned runs a handler only for whoever holds the workspace's token: the
// browser that started it, by its cookie, or an agent or other client the
// owner gave the token to, as a bearer token. A bearer token is never sent
// by a browser on its own, so another site cannot borrow it. Any other
// request, including one for a workspace that has expired or never
// existed, gets the same 404.
func (s *Server) owned(h ownedHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("ws")
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if token == r.Header.Get("Authorization") {
			token = ""
		}
		if c, err := r.Cookie(cookieName(id)); token == "" && err == nil {
			token = c.Value
		}
		if token == "" {
			s.notFound(w, r)
			return
		}
		ws, err := s.store.Workspace(r.Context(), id, token)
		if err != nil {
			s.notFound(w, r)
			return
		}
		h(w, r, ws)
	}
}

// === The Windowed and Classic views ========================================

// A workspace's own address is its Windowed view, the desktop, unless the
// reader has chosen Classic, which this browser remembers in a cookie.
// ?view=classic and ?view=window make the choice; both then go to the
// chosen view's plain address.
func (s *Server) workspaceRoot(w http.ResponseWriter, r *http.Request, ws *store.Workspace) {
	base := "/w/" + ws.ID + "/"
	switch r.URL.Query().Get("view") {
	case "classic":
		http.SetCookie(w, &http.Cookie{Name: "view", Value: "classic", Path: base, HttpOnly: true, Secure: s.Secure,
			SameSite: http.SameSiteLaxMode, Expires: ws.Expires().Add(time.Hour)})
		http.Redirect(w, r, base+"files/", http.StatusSeeOther)
		return
	case "window":
		http.SetCookie(w, &http.Cookie{Name: "view", Path: base, MaxAge: -1, HttpOnly: true, Secure: s.Secure, SameSite: http.SameSiteLaxMode})
		http.Redirect(w, r, base, http.StatusSeeOther)
		return
	}
	if c, err := r.Cookie("view"); err == nil && c.Value == "classic" {
		http.Redirect(w, r, base+"files/", http.StatusSeeOther)
		return
	}
	/* The windows the address names, or with none named the root folder's,
	   are rendered into the page, as PUDL's window contract asks of a
	   server, so the desktop holds its content without script, and a client
	   that only follows links and forms finds them there. */
	keys := []string{"r-" + ws.Root}
	if q := r.URL.Query(); q.Has("open") {
		keys = nil
		for _, k := range strings.Split(q.Get("open"), ",") {
			if kind, _, _ := parseKey(k); kind != "" {
				keys = append(keys, k)
			}
		}
	}
	var wins strings.Builder
	for _, k := range keys {
		rec := httptest.NewRecorder()
		r2 := r.Clone(r.Context())
		r2.SetPathValue("key", k)
		s.window(rec, r2, ws)
		if rec.Code == http.StatusOK {
			wins.WriteString(rec.Body.String())
		}
	}
	s.render(w, r, http.StatusOK, "desktop", map[string]any{
		"Title": "PUDL Desktop", "Desktop": true, "RootKey": "r-" + ws.Root,
		"RootURL": base, "RootPage": base + "files/", "Windows": template.HTML(wins.String()),
	})
}

// A window's key names a resource and the view of it the window shows:
// r-<id> a folder or a file, edit-<id> its editor, read-<id> its rendered
// form, hist-<id> its revisions, and rev-<id>-<n> one revision. Keys are
// what PUDL's windows write in the address, so they hold only letters,
// digits and hyphens, which entry ids are made of.
func parseKey(key string) (kind, id, n string) {
	if key == "agent" || key == "palette" {
		return key, "", ""
	}
	parts := strings.Split(key, "-")
	switch {
	case len(parts) == 2 && (parts[0] == "r" || parts[0] == "edit" || parts[0] == "read" || parts[0] == "hist" || parts[0] == "design"):
		return parts[0], parts[1], ""
	case len(parts) == 3 && parts[0] == "rev":
		return parts[0], parts[1], parts[2]
	case len(parts) == 3 && parts[0] == "node":
		// A node's path is written with underscores, since keys hold no dots.
		return parts[0], parts[1], strings.ReplaceAll(parts[2], "_", ".")
	}
	return "", "", ""
}

// keyURL is the Classic page a window's key names.
func keyURL(ws, key string) string {
	kind, id, n := parseKey(key)
	base := "/w/" + ws + "/r/" + id
	switch kind {
	case "r":
		return base
	case "edit":
		return base + "/edit"
	case "read":
		return base + "/rendered"
	case "hist":
		return base + "/revisions"
	case "rev":
		return base + "/revisions/" + n
	case "agent":
		return "/w/" + ws + "/agent"
	case "palette":
		return "/w/" + ws + "/palette"
	case "design":
		return base + "/design"
	case "node":
		return base + "/design/" + n
	}
	return ""
}

type windowCtx struct{}

// window serves a resource as a window of the Windowed view, the markup
// PUDL's windows fetch by key. It is the same resource as its Classic page,
// in another representation.
func (s *Server) window(w http.ResponseWriter, r *http.Request, ws *store.Workspace) {
	key := r.PathValue("key")
	kind, id, n := parseKey(key)
	r2 := r.Clone(context.WithValue(r.Context(), windowCtx{}, key))
	r2.SetPathValue("id", id)
	r2.SetPathValue("n", n)
	switch kind {
	case "r":
		s.byID(w, r2, ws)
	case "edit":
		s.editForm(w, r2, ws)
	case "read":
		s.rendered(w, r2, ws)
	case "hist":
		s.history(w, r2, ws)
	case "rev":
		s.revision(w, r2, ws)
	case "agent":
		s.agent(w, r2, ws)
	case "palette":
		s.paletteView(w, r2, ws)
	case "design":
		s.designView(w, r2, ws)
	case "node":
		r2.SetPathValue("path", n)
		s.nodeView(w, r2, ws)
	default:
		s.notFound(w, r)
	}
}

// windowed says whether a response is wanted as a window, and the key of
// the window asking: a request by key, or a form sent from inside a window,
// which says so with the X-PUDL-Window header so that its result comes back
// as the window's content rather than as a page.
func windowed(r *http.Request) (string, bool) {
	if k, ok := r.Context().Value(windowCtx{}).(string); ok {
		return k, true
	}
	if k := r.Header.Get("X-PUDL-Window"); k != "" {
		if kind, _, _ := parseKey(k); kind != "" {
			return k, true
		}
	}
	return "", false
}

// agent says how to let an AI agent work in this workspace: the MCP
// endpoint, and the workspace's token to give it as a bearer token. Only
// the workspace's holder can see the page, and the token is the one they
// already hold.
func (s *Server) agent(w http.ResponseWriter, r *http.Request, ws *store.Workspace) {
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if c, err := r.Cookie(cookieName(ws.ID)); err == nil {
		token = c.Value
	}
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	origin := scheme + "://" + r.Host
	s.render(w, r, http.StatusOK, "agent", map[string]any{
		"Title": "Connect an agent", "Key": "agent", "Endpoint": origin + "/mcp", "Token": token,
		"Workspace": origin + "/w/" + ws.ID + "/", "Root": "/w/" + ws.ID + "/r/" + ws.Root,
	})
}

// === Addresses =============================================================

func idURL(e *store.Entry) string { return "/w/" + e.Workspace + "/r/" + e.ID }

func (s *Server) pathURL(ctx context.Context, e *store.Entry) string {
	names, err := s.store.Path(ctx, e)
	if err != nil {
		return idURL(e)
	}
	parts := make([]string, len(names))
	for i, n := range names {
		parts[i] = url.PathEscape(n)
	}
	u := "/w/" + e.Workspace + "/files/" + strings.Join(parts, "/")
	if e.Kind == store.Folder && len(parts) > 0 {
		u += "/"
	}
	return u
}

func (s *Server) byPath(w http.ResponseWriter, r *http.Request, ws *store.Workspace) {
	e, err := s.store.Resolve(r.Context(), ws, strings.Split(r.PathValue("path"), "/"))
	if err != nil {
		s.notFound(w, r)
		return
	}
	s.show(w, r, e)
}

func (s *Server) byID(w http.ResponseWriter, r *http.Request, ws *store.Workspace) {
	e, err := s.entry(r, ws)
	if err != nil {
		s.notFound(w, r)
		return
	}
	s.show(w, r, e)
}

func (s *Server) entry(r *http.Request, ws *store.Workspace) (*store.Entry, error) {
	return s.store.Entry(r.Context(), ws.ID, r.PathValue("id"))
}

func (s *Server) show(w http.ResponseWriter, r *http.Request, e *store.Entry) {
	if e.Kind == store.Folder {
		s.showFolder(w, r, e, http.StatusOK, "", "", "")
	} else {
		s.showFile(w, r, e)
	}
}

// crumbs are the links from the root down to an entry's folder.
type crumb struct{ Name, URL string }

func (s *Server) crumbs(ctx context.Context, e *store.Entry) []crumb {
	var out []crumb
	for p := e; p.Parent != ""; {
		parent, err := s.store.Entry(ctx, p.Workspace, p.Parent)
		if err != nil {
			break
		}
		name := parent.Name
		if parent.Parent == "" {
			name = "Workspace"
		}
		out = append([]crumb{{name, s.pathURL(ctx, parent)}}, out...)
		p = parent
	}
	return out
}

// === Folders ===============================================================

type item struct {
	Entry    *store.Entry
	URL, Key string
}

func (s *Server) showFolder(w http.ResponseWriter, r *http.Request, f *store.Entry, status int, problem, name, kind string) {
	ctx := r.Context()
	children, err := s.store.Children(ctx, f)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	items := make([]item, len(children))
	for i, c := range children {
		items[i] = item{c, s.pathURL(ctx, c), "r-" + c.ID}
	}
	title := f.Name
	if f.Parent == "" {
		title = "Workspace"
	}
	var up string
	if f.Parent != "" {
		if p, err := s.store.Entry(ctx, f.Workspace, f.Parent); err == nil {
			up = s.pathURL(ctx, p)
		}
	}
	if kind == "" {
		kind = store.File
	}
	s.render(w, r, status, "folder", map[string]any{
		"Title": title, "Folder": f, "Items": items, "Up": up,
		"Canonical": idURL(f), "Path": s.pathURL(ctx, f), "Crumbs": s.crumbs(ctx, f),
		"Submission": store.NewID(16), "Problem": problem, "Name": name, "Kind": kind,
		"Key": "r-" + f.ID,
	})
}

func (s *Server) create(w http.ResponseWriter, r *http.Request, ws *store.Workspace) {
	folder, err := s.entry(r, ws)
	if err != nil {
		s.notFound(w, r)
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	kind := r.FormValue("kind")
	loc, _, err := s.store.Once(r.Context(), ws.ID, r.FormValue("submission"), func(tx *sql.Tx) (string, error) {
		e, err := s.store.Create(tx, folder, name, kind)
		if err != nil {
			return "", err
		}
		return idURL(e), nil
	})
	if errors.Is(err, store.ErrName) || errors.Is(err, store.ErrExists) || errors.Is(err, store.ErrNotDir) || errors.Is(err, store.ErrEntries) {
		s.showFolder(w, r, folder, http.StatusUnprocessableEntity, err.Error(), name, kind)
		return
	} else if err != nil {
		s.fail(w, r, err)
		return
	}
	http.Redirect(w, r, loc, http.StatusSeeOther)
}

// === Files =================================================================

func (s *Server) fileLinks(ctx context.Context, f *store.Entry) map[string]any {
	var up string
	if p, err := s.store.Entry(ctx, f.Workspace, f.Parent); err == nil {
		up = s.pathURL(ctx, p)
	}
	return map[string]any{
		"Title": f.Name, "File": f, "Up": up, "Crumbs": s.crumbs(ctx, f),
		"Canonical": idURL(f), "Path": s.pathURL(ctx, f),
		"History": idURL(f) + "/revisions", "Latest": fmt.Sprintf("%s/revisions/%d", idURL(f), f.Latest),
		"Raw": idURL(f) + "/raw", "Rendered": idURL(f) + "/rendered", "Edit": idURL(f) + "/edit",
		"Key": "r-" + f.ID, "FileKey": "r-" + f.ID, "EditKey": "edit-" + f.ID, "ReadKey": "read-" + f.ID,
		"HistoryKey": "hist-" + f.ID, "LatestKey": fmt.Sprintf("rev-%s-%d", f.ID, f.Latest),
		"IsHTML": isHTML(f), "Design": idURL(f) + "/design", "DesignKey": "design-" + f.ID,
	}
}

func (s *Server) showFile(w http.ResponseWriter, r *http.Request, f *store.Entry) {
	rev, err := s.store.Revision(r.Context(), f, f.Latest)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	data := s.fileLinks(r.Context(), f)
	data["Revision"] = rev
	s.render(w, r, http.StatusOK, "file", data)
}

func (s *Server) editForm(w http.ResponseWriter, r *http.Request, ws *store.Workspace) {
	f, err := s.entry(r, ws)
	if err != nil || f.Kind != store.File {
		s.notFound(w, r)
		return
	}
	rev, err := s.store.Revision(r.Context(), f, f.Latest)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	text, err := s.store.Read(f, rev)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.showEdit(w, r, f, http.StatusOK, f.Latest, string(text), "")
}

func (s *Server) showEdit(w http.ResponseWriter, r *http.Request, f *store.Entry, status, base int, text, problem string) {
	data := s.fileLinks(r.Context(), f)
	data["Title"] = "Edit " + f.Name
	data["Key"] = "edit-" + f.ID
	data["Base"], data["Text"], data["Problem"] = base, text, problem
	data["Submission"] = store.NewID(16)
	s.render(w, r, status, "edit", data)
}

func (s *Server) save(w http.ResponseWriter, r *http.Request, ws *store.Workspace) {
	f, err := s.entry(r, ws)
	if err != nil || f.Kind != store.File {
		s.notFound(w, r)
		return
	}
	base, err := strconv.Atoi(r.FormValue("base"))
	if err != nil {
		http.Error(w, "The form's base field must be a revision number.", http.StatusBadRequest)
		return
	}
	// A textarea sends CR LF line ends; a document keeps LF.
	text := strings.ReplaceAll(r.FormValue("text"), "\r\n", "\n")
	loc, _, err := s.store.Once(r.Context(), ws.ID, r.FormValue("submission"), func(tx *sql.Tx) (string, error) {
		if _, err := s.store.Save(tx, f, base, []byte(text)); err != nil {
			return "", err
		}
		return idURL(f), nil
	})
	var conflict *store.Conflict
	switch {
	case errors.As(err, &conflict):
		s.showConflict(w, r, f, conflict, text)
	case errors.Is(err, store.ErrQuota), errors.Is(err, store.ErrNotText):
		s.showEdit(w, r, f, http.StatusUnprocessableEntity, base, text, err.Error())
	case err != nil:
		s.fail(w, r, err)
	default:
		http.Redirect(w, r, loc, http.StatusSeeOther)
	}
}

// showConflict shows a save refused because the file moved on, with the
// latest text beside the writer's, and a form to save the writer's text
// against the latest revision once they have looked. The writer's text is
// in that form, so nothing is lost.
func (s *Server) showConflict(w http.ResponseWriter, r *http.Request, f *store.Entry, c *store.Conflict, text string) {
	rev, err := s.store.Revision(r.Context(), f, c.Latest)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	latest, err := s.store.Read(f, rev)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	data := s.fileLinks(r.Context(), f)
	data["Title"] = "Conflict in " + f.Name
	data["Key"] = "edit-" + f.ID
	data["Conflict"], data["LatestText"], data["Text"] = c, string(latest), text
	data["Base"] = c.Latest
	data["Submission"] = store.NewID(16)
	s.render(w, r, http.StatusConflict, "conflict", data)
}

func (s *Server) history(w http.ResponseWriter, r *http.Request, ws *store.Workspace) {
	f, err := s.entry(r, ws)
	if err != nil || f.Kind != store.File {
		s.notFound(w, r)
		return
	}
	revs, err := s.store.Revisions(r.Context(), f)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	data := s.fileLinks(r.Context(), f)
	data["Title"] = "Revisions of " + f.Name
	data["Key"] = "hist-" + f.ID
	data["Revisions"] = revs
	s.render(w, r, http.StatusOK, "history", data)
}

func (s *Server) revision(w http.ResponseWriter, r *http.Request, ws *store.Workspace) {
	f, err := s.entry(r, ws)
	if err != nil || f.Kind != store.File {
		s.notFound(w, r)
		return
	}
	n, err := strconv.Atoi(r.PathValue("n"))
	if err != nil {
		s.notFound(w, r)
		return
	}
	rev, err := s.store.Revision(r.Context(), f, n)
	if err != nil {
		s.notFound(w, r)
		return
	}
	text, err := s.store.Read(f, rev)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	data := s.fileLinks(r.Context(), f)
	data["Title"] = fmt.Sprintf("%s, revision %d", f.Name, n)
	data["Key"] = fmt.Sprintf("rev-%s-%d", f.ID, n)
	data["Revision"], data["Text"] = rev, string(text)
	if n > 1 {
		data["Predecessor"] = fmt.Sprintf("%s/revisions/%d", idURL(f), n-1)
		data["PredecessorKey"] = fmt.Sprintf("rev-%s-%d", f.ID, n-1)
	}
	s.render(w, r, http.StatusOK, "revision", data)
}

// raw returns a revision's Markdown source, the latest unless ?revision=
// asks for another.
func (s *Server) raw(w http.ResponseWriter, r *http.Request, ws *store.Workspace) {
	f, rev, ok := s.fileRevision(w, r, ws)
	if !ok {
		return
	}
	text, err := s.store.Read(f, rev)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Header().Set("ETag", fmt.Sprintf(`"%s-%d"`, f.ID, rev.Number))
	w.Write(text)
}

func (s *Server) rendered(w http.ResponseWriter, r *http.Request, ws *store.Workspace) {
	f, rev, ok := s.fileRevision(w, r, ws)
	if !ok {
		return
	}
	text, err := s.store.Read(f, rev)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	doc, err := markdown.Render(text)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	data := s.fileLinks(r.Context(), f)
	data["Revision"] = rev
	data["Doc"] = template.HTML(doc.HTML)
	data["Meta"] = doc.Meta
	if r.URL.Query().Get("revision") == "" {
		data["Key"] = "read-" + f.ID
	} else {
		data["Key"] = ""
	}
	if t, ok := doc.Meta["title"].(string); ok && t != "" {
		data["Title"] = t
	}
	s.render(w, r, http.StatusOK, "rendered", data)
}

func (s *Server) fileRevision(w http.ResponseWriter, r *http.Request, ws *store.Workspace) (*store.Entry, *store.Revision, bool) {
	f, err := s.entry(r, ws)
	if err != nil || f.Kind != store.File {
		s.notFound(w, r)
		return nil, nil, false
	}
	n := f.Latest
	if q := r.URL.Query().Get("revision"); q != "" {
		if n, err = strconv.Atoi(q); err != nil {
			s.notFound(w, r)
			return nil, nil, false
		}
	}
	rev, err := s.store.Revision(r.Context(), f, n)
	if err != nil {
		s.notFound(w, r)
		return nil, nil, false
	}
	return f, rev, true
}

// === Relations and errors ==================================================

func (s *Server) relation(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	text, ok := relations[name]
	if !ok {
		s.notFound(w, r)
		return
	}
	s.render(w, r, http.StatusOK, "relation", map[string]any{"Title": RelBase + name, "Name": name, "Text": text})
}

func (s *Server) notFound(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, http.StatusNotFound, "problem", map[string]any{
		"Title":   "Not found",
		"Message": "There is nothing here, or it belongs to a workspace this browser did not start, or the workspace has expired.",
	})
}

func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error) {
	log.Printf("%s %s: %v", r.Method, r.URL.Path, err)
	s.render(w, r, http.StatusInternalServerError, "problem", map[string]any{
		"Title": "Something went wrong", "Message": "The desktop could not do that. Nothing was changed.",
	})
}

// render renders a page's body, then wraps it: as a window when one asks
// for it, and otherwise as a Classic page. The desktop is a page of its own.
func (s *Server) render(w http.ResponseWriter, r *http.Request, status int, page string, data map[string]any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if strings.HasPrefix(r.URL.Path, "/w/") {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Vary", "X-PUDL-Window")
	}
	data["Ws"] = r.PathValue("ws")
	asking, win := windowed(r)
	wrapper := "page"
	if page == "desktop" {
		wrapper = "desktop"
	} else {
		data["Win"] = win
		var body strings.Builder
		if err := s.pages.ExecuteTemplate(&body, "body-"+page, data); err != nil {
			log.Printf("render %s: %v", page, err)
		}
		data["Body"] = template.HTML(body.String())
		if win {
			wrapper = "window"
			// A window shows the resource the response is about, which after a
			// form sent from a window may be another than the one that asked.
			if k, _ := data["Key"].(string); k == "" {
				data["Key"] = asking
			}
			data["Self"] = keyURL(data["Ws"].(string), data["Key"].(string))
		}
	}
	w.WriteHeader(status)
	if err := s.pages.ExecuteTemplate(w, wrapper, data); err != nil {
		log.Printf("render %s: %v", wrapper, err)
	}
}

func humanSize(n int64) string {
	// One decimal place, left out when it is zero: 5 MB, 1.5 KB.
	short := func(f float64, unit string) string {
		return strings.TrimSuffix(fmt.Sprintf("%.1f", f), ".0") + " " + unit
	}
	switch {
	case n >= 1<<20:
		return short(float64(n)/(1<<20), "MB")
	case n >= 1<<10:
		return short(float64(n)/(1<<10), "KB")
	default:
		return fmt.Sprintf("%d bytes", n)
	}
}
