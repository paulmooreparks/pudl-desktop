// Copyright 2026 Paul Parks. SPDX-License-Identifier: Apache-2.0

package web

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/paulmooreparks/pudl-desktop/internal/design"
	"github.com/paulmooreparks/pudl-desktop/internal/store"
)

// === The palette ===========================================================
//
// PUDL Studio's palette is PUDL's component list (dist/pudl-components.json
// in the vendored release), grouped by the grammar's categories.

type component struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	Spec     string `json:"spec"`
	Template string `json:"template"`
	Context  string `json:"context"`
	Parts    []struct {
		Selector string `json:"selector"`
	} `json:"parts"`
	Variants []variant `json:"variants"`
	States   []state   `json:"states"`
}

// A variant is a class that changes how a component looks. Variants with a
// group are alternatives; one that replaces a class takes its place.
type variant struct {
	Name     string `json:"name"`
	Class    string `json:"class"`
	Group    string `json:"group"`
	Replaces string `json:"replaces"`
	On       string `json:"on"`
}

// A state is an attribute that says what condition a component is in. On,
// it has its value (none, for a boolean attribute); off, it has its off
// value or is gone.
type state struct {
	Name      string `json:"name"`
	Attribute string `json:"attribute"`
	Value     string `json:"value"`
	Off       string `json:"off"`
	On        string `json:"on"`
}

type componentList struct {
	Grammar    map[string]string `json:"grammar"`
	Components []component       `json:"components"`
}

var (
	paletteOnce sync.Once
	palette     componentList
	byID        map[string]component
)

// The grammar's categories in the order the palette shows them.
var kindOrder = []string{"raised", "sunken", "flat", "list", "tab", "link", "handle", "layout"}

func loadPalette() {
	paletteOnce.Do(func() {
		raw, err := staticFS.ReadFile("static/pudl/pudl-components.json")
		if err == nil {
			json.Unmarshal(raw, &palette)
		}
		byID = map[string]component{}
		for _, c := range palette.Components {
			byID[c.ID] = c
		}
	})
}

type paletteGroup struct {
	Kind, About string
	Components  []component
}

func paletteGroups() []paletteGroup {
	loadPalette()
	var out []paletteGroup
	for _, k := range kindOrder {
		g := paletteGroup{Kind: k, About: palette.Grammar[k]}
		for _, c := range palette.Components {
			if c.Kind == k {
				g.Components = append(g.Components, c)
			}
		}
		if len(g.Components) > 0 {
			sort.Slice(g.Components, func(a, b int) bool { return g.Components[a].Name < g.Components[b].Name })
			out = append(out, g)
		}
	}
	return out
}

func (s *Server) paletteView(w http.ResponseWriter, r *http.Request, ws *store.Workspace) {
	s.render(w, r, http.StatusOK, "palette", map[string]any{
		"Title": "Palette", "Key": "palette", "Groups": paletteGroups(),
	})
}

// === Designing a document ==================================================

func isHTML(e *store.Entry) bool {
	n := strings.ToLower(e.Name)
	return e.Kind == store.File && (strings.HasSuffix(n, ".html") || strings.HasSuffix(n, ".htm"))
}

func nodeKey(id, path string) string {
	if path == "" {
		return "design-" + id
	}
	return "node-" + id + "-" + strings.ReplaceAll(path, ".", "_")
}

func (s *Server) designURL(f *store.Entry, path string) string {
	if path == "" {
		return idURL(f) + "/design"
	}
	return idURL(f) + "/design/" + path
}

// document reads a file's latest revision as a document.
func (s *Server) document(w http.ResponseWriter, r *http.Request, ws *store.Workspace) (*store.Entry, *design.Doc, bool) {
	f, err := s.entry(r, ws)
	if err != nil || !isHTML(f) {
		s.notFound(w, r)
		return nil, nil, false
	}
	rev, err := s.store.Revision(r.Context(), f, f.Latest)
	if err != nil {
		s.fail(w, r, err)
		return nil, nil, false
	}
	text, err := s.store.Read(f, rev)
	if err != nil {
		s.fail(w, r, err)
		return nil, nil, false
	}
	d, err := design.Parse(text)
	if err != nil {
		s.fail(w, r, err)
		return nil, nil, false
	}
	return f, d, true
}

type treeRow struct {
	Depth          int
	Path, Key, URL string
	Label, Text    string
}

func (s *Server) rows(f *store.Entry, nodes []design.Node, depth int) []treeRow {
	var out []treeRow
	for _, n := range nodes {
		out = append(out, treeRow{depth, n.Path, nodeKey(f.ID, n.Path), s.designURL(f, n.Path), design.Describe(n), n.Text})
		out = append(out, s.rows(f, n.Children, depth+1)...)
	}
	return out
}

func (s *Server) designView(w http.ResponseWriter, r *http.Request, ws *store.Workspace) {
	f, d, ok := s.document(w, r, ws)
	if !ok {
		return
	}
	s.showDesign(w, r, f, d, http.StatusOK, "")
}

func (s *Server) showDesign(w http.ResponseWriter, r *http.Request, f *store.Entry, d *design.Doc, status int, problem string) {
	tree, _ := d.Tree("")
	data := s.fileLinks(r.Context(), f)
	data["Title"] = "Design " + f.Name
	data["Key"] = nodeKey(f.ID, "")
	data["Rows"] = s.rows(f, tree, 0)
	data["Source"] = string(d.Render())
	data["Problem"] = problem
	s.opForms(data, f, "")
	s.render(w, r, status, "design", data)
}

// opForms gives a page the fields every operation's form carries.
func (s *Server) opForms(data map[string]any, f *store.Entry, path string) {
	data["Base"] = f.Latest
	data["NodePath"] = path
	data["Ops"] = idURL(f) + "/design/ops/"
	data["Submission"] = store.NewID(16)
	data["Palette"] = paletteGroups()
}

func (s *Server) nodeView(w http.ResponseWriter, r *http.Request, ws *store.Workspace) {
	f, d, ok := s.document(w, r, ws)
	if !ok {
		return
	}
	s.showNode(w, r, f, d, r.PathValue("path"), http.StatusOK, "")
}

func (s *Server) showNode(w http.ResponseWriter, r *http.Request, f *store.Entry, d *design.Doc, path string, status int, problem string) {
	n, err := d.Element(path)
	if err != nil {
		s.notFound(w, r)
		return
	}
	data := s.fileLinks(r.Context(), f)
	data["Title"] = design.Describe(n) + " in " + f.Name
	data["Key"] = nodeKey(f.ID, path)
	data["Node"] = n
	data["Label"] = design.Describe(n)
	parent := ""
	if i := strings.LastIndex(path, "."); i >= 0 {
		parent = path[:i]
	}
	data["ParentURL"], data["ParentKey"] = s.designURL(f, parent), nodeKey(f.ID, parent)
	var kids []treeRow
	for _, c := range n.Children {
		kids = append(kids, treeRow{0, c.Path, nodeKey(f.ID, c.Path), s.designURL(f, c.Path), design.Describe(c), c.Text})
	}
	data["Kids"] = kids
	data["Props"] = propForms(d, path)
	data["Problem"] = problem
	s.opForms(data, f, path)
	s.render(w, r, status, "node", data)
}

// operate carries out one operation on a document against the revision
// the form was read at, as a new revision, and shows the element it
// leaves in view. A path is only meaningful at the revision it was read
// at, so an operation against an older one is refused with 409.
func (s *Server) operate(w http.ResponseWriter, r *http.Request, ws *store.Workspace) {
	f, err := s.entry(r, ws)
	if err != nil || !isHTML(f) {
		s.notFound(w, r)
		return
	}
	op := r.PathValue("op")
	base, err := strconv.Atoi(r.FormValue("base"))
	if err != nil {
		http.Error(w, "The form's base field must be a revision number.", http.StatusBadRequest)
		return
	}
	path, err := design.ParsePath(r.FormValue("path"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	// The revision the form was read at is read before the change begins:
	// a revision never changes once written, and the change's transaction
	// holds the store's one connection. Saving against it refuses the
	// change if the document has moved on.
	shown := path
	apply := func() ([]byte, error) {
		rev, err := s.store.Revision(r.Context(), f, base)
		if err != nil {
			return nil, &store.Conflict{Base: base, Latest: f.Latest}
		}
		text, err := s.store.Read(f, rev)
		if err != nil {
			return nil, err
		}
		d, err := design.Parse(text)
		if err != nil {
			return nil, err
		}
		switch op {
		case "set-attribute":
			err = d.SetAttr(path, r.FormValue("name"), r.FormValue("value"))
		case "remove-attribute":
			err = d.RemoveAttr(path, r.FormValue("name"))
		case "properties":
			if path == "" {
				return nil, fmt.Errorf("%w: the document as a whole has no properties", errOp)
			}
			err = setProperties(d, path, r.PostForm)
		case "set-text":
			err = d.SetText(path, strings.ReplaceAll(r.FormValue("text"), "\r\n", "\n"))
		case "insert":
			markup := r.FormValue("markup")
			if id := r.FormValue("component"); id != "" {
				loadPalette()
				c, ok := byID[id]
				if !ok {
					return nil, fmt.Errorf("%w: there is no component %q", errOp, id)
				}
				markup = c.Template
			}
			shown, err = d.Insert(path, r.FormValue("position"), []byte(markup))
		case "move":
			target, perr := design.ParsePath(r.FormValue("target"))
			if perr != nil {
				return nil, fmt.Errorf("%w: %v", errOp, perr)
			}
			shown, err = d.Move(path, target, r.FormValue("position"))
		case "remove":
			err = d.Remove(path)
			if i := strings.LastIndex(path, "."); i >= 0 {
				shown = path[:i]
			} else {
				shown = ""
			}
		default:
			return nil, fmt.Errorf("%w: there is no operation %q", errOp, op)
		}
		if err != nil {
			return nil, fmt.Errorf("%w: %v", errOp, err)
		}
		return d.Render(), nil
	}
	changed, err := apply()
	var loc string
	if err == nil {
		loc, _, err = s.store.Once(r.Context(), ws.ID, r.FormValue("submission"), func(tx *sql.Tx) (string, error) {
			if _, err := s.store.Save(tx, f, base, changed); err != nil {
				return "", err
			}
			return s.designURL(f, shown), nil
		})
	}
	var conflict *store.Conflict
	switch {
	case errors.As(err, &conflict):
		_, latest, ok := s.document(w, r, ws)
		if !ok {
			return
		}
		f2, _ := s.entry(r, ws)
		s.showDesign(w, r, f2, latest, http.StatusConflict,
			fmt.Sprintf("The document is at revision %d, not %d, so the paths you read may point elsewhere now. Nothing was changed. Here it is as it stands.", conflict.Latest, conflict.Base))
	case errors.Is(err, errOp), errors.Is(err, store.ErrQuota):
		_, current, ok := s.document(w, r, ws)
		if !ok {
			return
		}
		msg := strings.TrimPrefix(err.Error(), errOp.Error()+": ")
		if _, e := current.Element(path); path == "" || e != nil {
			s.showDesign(w, r, f, current, http.StatusUnprocessableEntity, msg)
		} else {
			s.showNode(w, r, f, current, path, http.StatusUnprocessableEntity, msg)
		}
	case err != nil:
		s.fail(w, r, err)
	default:
		http.Redirect(w, r, loc, http.StatusSeeOther)
	}
}

var errOp = errors.New("the operation could not be carried out")
