// Copyright 2026 Paul Parks. SPDX-License-Identifier: Apache-2.0

package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"html/template"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// The applet packages installed on this desktop: each a folder under the
// applets directory (-applets) with an applet.json, the one file read here,
// as the package design describes (Architecture/applet-packages-design.md
// in the parkscomputing repo). From the manifests the desktop writes what
// a host writes for each applet: its define() in /applets/registry.js, its
// page at /app/{name}, its window app-{name}, and its entry in the Applets
// menu. The folder is read again when a manifest or a page body changes. A
// manifest that breaks the rules is left out, and said once in the log.

const appletsFolder = "applets"

var (
	appletName   = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)
	appletFile   = regexp.MustCompile(`^(?:[A-Za-z0-9._-]+/)*[A-Za-z0-9._-]+$`)
	appletShape  = regexp.MustCompile(`^(0?\.[0-9]+|1(\.0+)?),(0?\.[0-9]+|1(\.0+)?)$`)
	appletPixels = regexp.MustCompile(`^[1-9][0-9]*,[1-9][0-9]*$`)
)

// An applet package as its manifest says. Every file is relative to the
// package's folder.
type applet struct {
	Name, Title, Version, Description string
	Src, Css, Icon                    string
	Body, Fit                         string
	Shape, Min, Max                   string
	Handles                           json.RawMessage
	Instances                         int
	Actions                           string
	Dir                               string
}

// URL is a file of the package by its address on the desktop.
func (a applet) URL(file string) string { return "/" + appletsFolder + "/" + a.Name + "/" + file }

// IconURL is the package's icon, or empty.
func (a applet) IconURL() string {
	if a.Icon == "" {
		return ""
	}
	return a.URL(a.Icon)
}

type appletCatalog struct {
	mu     sync.Mutex
	stamp  string
	list   []applet
	warned map[string]string
}

type manifest struct {
	Name        string `json:"name"`
	Title       string `json:"title"`
	Version     string `json:"version"`
	Description string `json:"description"`
	Src         string `json:"src"`
	Css         string `json:"css"`
	Icon        string `json:"icon"`
	Page        *struct {
		Body string `json:"body"`
		Fit  string `json:"fit"`
	} `json:"page"`
	Window *struct {
		Shape string `json:"shape"`
		Min   string `json:"min"`
		Max   string `json:"max"`
	} `json:"window"`
	Handles   json.RawMessage `json:"handles"`
	Instances int             `json:"instances"`
	Actions   string          `json:"actions"`
}

// applets is the installed packages, in name order, read again when the
// folder has changed since.
func (s *Server) applets() []applet {
	if s.Applets == "" {
		return nil
	}
	c := &s.catalog
	c.mu.Lock()
	defer c.mu.Unlock()
	stamp := appletStamp(s.Applets)
	if stamp == c.stamp {
		return c.list
	}
	if c.warned == nil {
		c.warned = map[string]string{}
	}
	var list []applet
	entries, _ := os.ReadDir(s.Applets)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(s.Applets, e.Name())
		raw, err := os.ReadFile(filepath.Join(dir, "applet.json"))
		if err != nil {
			continue
		}
		a, err := readApplet(dir, e.Name(), raw)
		if err != nil {
			if c.warned[e.Name()] != err.Error() {
				log.Printf("the applet package %s was left out: %v", e.Name(), err)
				c.warned[e.Name()] = err.Error()
			}
			continue
		}
		delete(c.warned, e.Name())
		list = append(list, a)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	c.stamp, c.list = stamp, list
	return list
}

// appletStamp is what the folder holds that the catalogue depends on:
// each package's manifest and page body, with when they changed.
func appletStamp(dir string) string {
	var b strings.Builder
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		for _, f := range []string{"applet.json", "page.html"} {
			if fi, err := os.Stat(filepath.Join(dir, e.Name(), f)); err == nil {
				fmt.Fprintf(&b, "%s/%s:%d:%d;", e.Name(), f, fi.ModTime().UnixNano(), fi.Size())
			}
		}
	}
	return b.String()
}

// readApplet applies the manifest's rules, so the desktop trusts nothing
// it did not check: the name is the folder's, every file named is a
// relative path inside the package and is there.
func readApplet(dir, folder string, raw []byte) (applet, error) {
	var m manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return applet{}, fmt.Errorf("applet.json cannot be read: %v", err)
	}
	file := func(key, v string) (string, error) {
		if v == "" {
			return "", nil
		}
		if !appletFile.MatchString(v) || strings.Contains("/"+v+"/", "/../") || strings.Contains("/"+v+"/", "/./") {
			return "", fmt.Errorf("%s must be a relative path inside the package", key)
		}
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(v))); err != nil {
			return "", fmt.Errorf("%s names %s, which is not in the package", key, v)
		}
		return v, nil
	}
	if !appletName.MatchString(m.Name) {
		return applet{}, errors.New("name must be lower-case letters, digits and hyphens, starting with a letter")
	}
	if m.Name != folder {
		return applet{}, fmt.Errorf("name must be the folder's name, %s", folder)
	}
	if strings.TrimSpace(m.Title) == "" {
		return applet{}, errors.New("title is missing")
	}
	if strings.TrimSpace(m.Version) == "" {
		return applet{}, errors.New("version is missing")
	}
	a := applet{Name: m.Name, Title: strings.TrimSpace(m.Title), Version: strings.TrimSpace(m.Version), Description: m.Description, Dir: dir, Handles: m.Handles, Instances: m.Instances}
	var err error
	if m.Src == "" {
		return applet{}, errors.New("src is missing")
	}
	if a.Src, err = file("src", m.Src); err != nil {
		return applet{}, err
	}
	if a.Css, err = file("css", m.Css); err != nil {
		return applet{}, err
	}
	if a.Icon, err = file("icon", m.Icon); err != nil {
		return applet{}, err
	}
	if a.Actions, err = file("actions", m.Actions); err != nil {
		return applet{}, err
	}
	if m.Page != nil {
		if m.Page.Fit != "" && m.Page.Fit != "fill" && m.Page.Fit != "flow" {
			return applet{}, errors.New("page.fit must be fill or flow")
		}
		a.Fit = m.Page.Fit
		body, err := file("page.body", m.Page.Body)
		if err != nil {
			return applet{}, err
		}
		if body != "" {
			text, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(body)))
			if err != nil {
				return applet{}, err
			}
			a.Body = string(text)
		}
	}
	if m.Window != nil {
		if m.Window.Shape != "" && !appletShape.MatchString(m.Window.Shape) {
			return applet{}, errors.New("window.shape must be fractions of the desktop, such as 0.28,0.88")
		}
		if (m.Window.Min != "" && !appletPixels.MatchString(m.Window.Min)) || (m.Window.Max != "" && !appletPixels.MatchString(m.Window.Max)) {
			return applet{}, errors.New("window.min and window.max must be pixels, such as 420,220")
		}
		a.Shape, a.Min, a.Max = m.Window.Shape, m.Window.Min, m.Window.Max
	}
	if m.Instances < 0 {
		return applet{}, errors.New("instances must be a whole number from 1")
	}
	return a, nil
}

func (s *Server) appletByName(name string) (applet, bool) {
	for _, a := range s.applets() {
		if a.Name == name {
			return a, true
		}
	}
	return applet{}, false
}

// registryScript is /applets/registry.js: a define() for each package,
// with its files at their addresses, its page at /app/{name}, and its
// version as the cache stamp.
func (s *Server) registryScript(w http.ResponseWriter, r *http.Request) {
	var b strings.Builder
	b.WriteString("/* The applet packages installed on this desktop, from its applets folder's\n   <name>/applet.json. Written by the server; not a file. */\n")
	for _, a := range s.applets() {
		cfg := map[string]any{"src": a.URL(a.Src), "page": "/app/" + a.Name, "ver": a.Version}
		if a.Css != "" {
			cfg["css"] = a.URL(a.Css)
		}
		if len(a.Handles) > 0 {
			cfg["handles"] = json.RawMessage(a.Handles)
		}
		if a.Instances > 0 {
			cfg["instances"] = a.Instances
		}
		if a.Actions != "" {
			cfg["actions"] = a.URL(a.Actions)
		}
		name, _ := json.Marshal(a.Name)
		js, _ := json.Marshal(cfg)
		fmt.Fprintf(&b, "pudlApplets.define(%s, %s);\n", name, js)
	}
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write([]byte(b.String()))
}

// appletBody is the page's body for a package: the manifest's own, which
// the package wrote and the desktop trusts as it trusts the package's
// script, or a bare mount.
func appletBody(a applet) template.HTML {
	if a.Body != "" {
		return template.HTML(strings.TrimRight(a.Body, "\n") + "\n")
	}
	fit := ""
	if a.Fit != "" {
		fit = ` data-applet-fit="` + a.Fit + `"`
	}
	t := html.EscapeString(a.Title)
	return template.HTML(`<div data-applet="` + a.Name + `"` + fit + ` role="application" aria-label="` + t + `"><noscript><p>` + t + ` needs JavaScript to run.</p></noscript></div>` + "\n")
}

// appletPage serves /app/{name}, the package's Classic page.
func (s *Server) appletPage(w http.ResponseWriter, r *http.Request) {
	a, ok := s.appletByName(r.PathValue("name"))
	if !ok {
		s.notFound(w, r)
		return
	}
	s.render(w, r, http.StatusOK, "applet", map[string]any{
		"Title": a.Title, "Applet": true, "Mount": appletBody(a), "WinMin": a.Min, "WinMax": a.Max,
	})
}

// appletWindow serves a package's window, key app-{name}.
func (s *Server) appletWindow(w http.ResponseWriter, r *http.Request, name string) {
	a, ok := s.appletByName(name)
	if !ok {
		s.notFound(w, r)
		return
	}
	s.render(w, r, http.StatusOK, "applet", map[string]any{
		"Title": a.Title, "Applet": true, "Mount": appletBody(a), "Key": "app-" + a.Name, "WinMin": a.Min, "WinMax": a.Max,
	})
}

// appletEntries is what the Applets menu lists for the packages.
func (s *Server) appletEntries() []map[string]string {
	var out []map[string]string
	for _, a := range s.applets() {
		out = append(out, map[string]string{"Name": a.Name, "Title": a.Title, "Icon": a.IconURL(), "Description": a.Description})
	}
	return out
}

// appletFiles serves the packages' files from the applets folder.
func (s *Server) appletFiles() http.Handler {
	if s.Applets == "" {
		return http.NotFoundHandler()
	}
	return http.StripPrefix("/"+appletsFolder+"/", http.FileServerFS(os.DirFS(s.Applets)))
}
