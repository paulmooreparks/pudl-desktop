// Copyright 2026 Paul Parks. SPDX-License-Identifier: Apache-2.0

// Package mcp lets an AI agent drive PUDL Desktop through the Model
// Context Protocol, over its Streamable HTTP transport. It offers no tool
// for any one feature. Its tools are those of a client that only follows
// links and fills forms: open a page and read it as its words, links and
// forms, and send a form. An agent therefore does what the pages offer and
// nothing else, as the browser and the terminal do, and a new feature
// reaches agents the moment it has a page.
//
// The server answers each call by sending the request to the desktop in
// the same process, as any client's request, keeping a session's cookies
// as a browser keeps them and passing on the bearer token the agent was
// configured with, which is how an agent works in a person's workspace.
package mcp

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/paulmooreparks/pudl-desktop/internal/hyper"
)

// The protocol versions this server speaks, newest first.
var versions = []string{"2025-06-18", "2025-03-26"}

const instructions = `PUDL Desktop is a place to write and keep Markdown documents, and PUDL Studio builds applets and sites in it. It is driven entirely by hypermedia: every page says what can be done next as links and forms, and anything not offered cannot be done. Use "open" to read a page and "submit" to send one of its forms, filling the fields you need and keeping the others, hidden ones included, as the page gave them. Start at "/" to start a temporary workspace with its form, unless you were given a workspace's bearer token, in which case open the workspace's address. A form's "base" field is the revision you read; if the document has moved on, the reply is a 409 conflict page with your text kept, and you send that page's form after looking at the latest text. Every change is a revision, so nothing is lost.`

type session struct {
	cookies map[string]*http.Cookie
	used    time.Time
}

// Server is the MCP endpoint. Site is the desktop it drives.
type Server struct {
	Site     http.Handler
	mu       sync.Mutex
	sessions map[string]*session
}

func New(site http.Handler) *Server {
	return &Server{Site: site, sessions: map[string]*session{}}
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
	case http.MethodDelete:
		s.mu.Lock()
		delete(s.sessions, r.Header.Get("Mcp-Session-Id"))
		s.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
		return
	default:
		// The server sends nothing of its own accord, so it offers no stream.
		w.Header().Set("Allow", "POST, DELETE")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req rpcRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 8<<20)).Decode(&req); err != nil || req.JSONRPC != "2.0" || req.Method == "" {
		reply(w, nil, nil, &rpcError{-32700, "The body is not a JSON-RPC 2.0 request."})
		return
	}
	// A notification, which has no id, is accepted and needs no answer.
	if len(req.ID) == 0 {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	if req.Method == "initialize" {
		s.initialize(w, req)
		return
	}
	sess := s.session(r.Header.Get("Mcp-Session-Id"))
	if sess == nil {
		w.WriteHeader(http.StatusNotFound)
		reply(w, req.ID, nil, &rpcError{-32001, "Unknown or expired session; initialize again."})
		return
	}
	switch req.Method {
	case "ping":
		reply(w, req.ID, struct{}{}, nil)
	case "tools/list":
		reply(w, req.ID, map[string]any{"tools": tools}, nil)
	case "tools/call":
		s.call(w, r, req, sess)
	default:
		reply(w, req.ID, nil, &rpcError{-32601, "No such method: " + req.Method})
	}
}

func (s *Server) initialize(w http.ResponseWriter, req rpcRequest) {
	var p struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	json.Unmarshal(req.Params, &p)
	version := versions[0]
	for _, v := range versions {
		if v == p.ProtocolVersion {
			version = v
		}
	}
	b := make([]byte, 16)
	rand.Read(b)
	id := hex.EncodeToString(b)
	s.mu.Lock()
	for k, old := range s.sessions {
		if time.Since(old.used) > 24*time.Hour {
			delete(s.sessions, k)
		}
	}
	s.sessions[id] = &session{cookies: map[string]*http.Cookie{}, used: time.Now()}
	s.mu.Unlock()
	w.Header().Set("Mcp-Session-Id", id)
	reply(w, req.ID, map[string]any{
		"protocolVersion": version,
		"capabilities":    map[string]any{"tools": map[string]any{}},
		"serverInfo":      map[string]any{"name": "pudl-desktop", "title": "PUDL Desktop", "version": "0.1.0"},
		"instructions":    instructions,
	}, nil)
}

func (s *Server) session(id string) *session {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess := s.sessions[id]
	if sess != nil {
		sess.used = time.Now()
	}
	return sess
}

var tools = []map[string]any{
	{
		"name":        "open",
		"title":       "Open a page",
		"description": "Opens a page of PUDL Desktop by its address and reads it as its title, its words, its links (each with its relation) and its forms (each with its fields and their current values). Follows redirects.",
		"inputSchema": map[string]any{
			"type":       "object",
			"properties": map[string]any{"url": map[string]any{"type": "string", "description": "The page's address, such as / or one from a link."}},
			"required":   []string{"url"},
		},
		"annotations": map[string]any{"readOnlyHint": true, "openWorldHint": false},
	},
	{
		"name":        "submit",
		"title":       "Send a form",
		"description": "Sends one of a page's forms to its action with its method, and reads the page that results, following the 303 that a change returns. Send every field the form has, hidden ones included, with the values the page gave except those you change.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{"type": "string", "description": "The form's action."},
				"method": map[string]any{"type": "string", "enum": []string{"GET", "POST"}, "description": "The form's method."},
				"fields": map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}, "description": "The fields to send, by name."},
			},
			"required": []string{"action", "method", "fields"},
		},
		"annotations": map[string]any{"readOnlyHint": false, "destructiveHint": false, "openWorldHint": false},
	},
}

func (s *Server) call(w http.ResponseWriter, r *http.Request, req rpcRequest, sess *session) {
	var p struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &p); err != nil {
		reply(w, req.ID, nil, &rpcError{-32602, "The call's parameters could not be read."})
		return
	}
	bearer := r.Header.Get("Authorization")
	var status int
	var page *hyper.Page
	var body string
	var err error
	switch p.Name {
	case "open":
		var a struct{ URL string }
		json.Unmarshal(p.Arguments, &a)
		status, page, body, err = s.fetch(r, sess, bearer, "GET", a.URL, nil)
	case "submit":
		var a struct {
			Action string            `json:"action"`
			Method string            `json:"method"`
			Fields map[string]string `json:"fields"`
		}
		json.Unmarshal(p.Arguments, &a)
		v := url.Values{}
		for k, val := range a.Fields {
			v.Set(k, val)
		}
		status, page, body, err = s.fetch(r, sess, bearer, strings.ToUpper(a.Method), a.Action, v)
	default:
		reply(w, req.ID, nil, &rpcError{-32602, "No such tool: " + p.Name})
		return
	}
	if err != nil {
		reply(w, req.ID, map[string]any{"content": []map[string]any{{"type": "text", "text": err.Error()}}, "isError": true}, nil)
		return
	}
	result := map[string]any{"content": []map[string]any{{"type": "text", "text": describe(status, page, body)}}}
	if page != nil {
		result["structuredContent"] = map[string]any{"status": status, "page": page}
	}
	reply(w, req.ID, result, nil)
}

// fetch sends a request to the desktop in this process, as a browser
// would, and follows redirects with GET, as a browser follows a 303.
func (s *Server) fetch(r *http.Request, sess *session, bearer, method, target string, form url.Values) (int, *hyper.Page, string, error) {
	if method != "GET" && method != "POST" {
		return 0, nil, "", fmt.Errorf("a form's method is GET or POST, not %q", method)
	}
	for hop := 0; hop < 10; hop++ {
		u, err := url.Parse(target)
		if err != nil || (u.Host != "" && u.Host != r.Host) || !strings.HasPrefix(u.Path, "/") {
			return 0, nil, "", fmt.Errorf("%q is not an address on this desktop", target)
		}
		var body io.Reader
		if method == "GET" && form != nil {
			q := u.Query()
			for k, v := range form {
				q[k] = v
			}
			u.RawQuery = q.Encode()
			form = nil
		} else if form != nil {
			body = strings.NewReader(form.Encode())
		}
		req := httptest.NewRequest(method, u.RequestURI(), body)
		req.Host = r.Host
		req.Header.Set("Accept", "text/html")
		if body != nil {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		if bearer != "" {
			req.Header.Set("Authorization", bearer)
		}
		s.mu.Lock()
		for _, c := range sess.cookies {
			if strings.HasPrefix(u.Path, c.Path) {
				req.AddCookie(&http.Cookie{Name: c.Name, Value: c.Value})
			}
		}
		s.mu.Unlock()
		rec := httptest.NewRecorder()
		s.Site.ServeHTTP(rec, req)
		res := rec.Result()
		s.mu.Lock()
		for _, c := range res.Cookies() {
			if c.Path == "" {
				c.Path = "/"
			}
			if c.MaxAge < 0 {
				delete(sess.cookies, c.Name+c.Path)
			} else {
				sess.cookies[c.Name+c.Path] = c
			}
		}
		s.mu.Unlock()
		if res.StatusCode >= 300 && res.StatusCode < 400 {
			loc, err := u.Parse(res.Header.Get("Location"))
			if err != nil {
				return 0, nil, "", err
			}
			target, method, form = loc.String(), "GET", nil
			continue
		}
		raw, _ := io.ReadAll(res.Body)
		if !strings.HasPrefix(res.Header.Get("Content-Type"), "text/html") {
			return res.StatusCode, nil, string(raw), nil
		}
		page, err := hyper.Read(bytes.NewReader(raw), "http://"+r.Host+u.RequestURI())
		if err != nil {
			return 0, nil, "", err
		}
		page.URL = u.RequestURI()
		return res.StatusCode, page, "", nil
	}
	return 0, nil, "", fmt.Errorf("too many redirects from %q", target)
}

// describe writes a page out for an agent to read: where it is, what it
// says, where its links go and what its forms take.
func describe(status int, p *hyper.Page, body string) string {
	if p == nil {
		return fmt.Sprintf("Status %d. The response is not a page; its content:\n\n%s", status, body)
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "%s\nAddress: %s\nStatus: %d %s\n\n", p.Title, p.URL, status, http.StatusText(status))
	if p.Text != "" {
		sb.WriteString(p.Text + "\n\n")
	}
	if len(p.Links) > 0 {
		sb.WriteString("Links:\n")
		for _, l := range p.Links {
			fmt.Fprintf(&sb, "- %s -> %s", orDash(l.Text), l.Href)
			if l.Rel != "" {
				fmt.Fprintf(&sb, " [rel %s]", l.Rel)
			}
			if l.Type != "" {
				fmt.Fprintf(&sb, " [type %s]", l.Type)
			}
			sb.WriteString("\n")
		}
		sb.WriteString("\n")
	}
	for i, f := range p.Forms {
		fmt.Fprintf(&sb, "Form %d: %s %s", i+1, f.Method, f.Action)
		if f.Rel != "" {
			fmt.Fprintf(&sb, " [rel %s]", f.Rel)
		}
		if len(f.Buttons) > 0 {
			names := make([]string, len(f.Buttons))
			for j, b := range f.Buttons {
				names[j] = b.Text
			}
			fmt.Fprintf(&sb, " (sent by: %s)", strings.Join(names, ", "))
		}
		sb.WriteString("\n")
		fields := append([]hyper.Field(nil), f.Fields...)
		sort.SliceStable(fields, func(a, b int) bool { return fields[a].Type == "hidden" && fields[b].Type != "hidden" })
		for _, fd := range fields {
			fmt.Fprintf(&sb, "  - %s (%s", fd.Name, fd.Type)
			if fd.Required {
				sb.WriteString(", required")
			}
			if fd.Type == "checkbox" || fd.Type == "radio" {
				fmt.Fprintf(&sb, ", checked: %v", fd.Checked)
			}
			sb.WriteString(")")
			if fd.Label != "" {
				fmt.Fprintf(&sb, " %q", fd.Label)
			}
			if len(fd.Options) > 0 {
				opts := make([]string, len(fd.Options))
				for j, o := range fd.Options {
					opts[j] = o.Value
				}
				fmt.Fprintf(&sb, " one of: %s", strings.Join(opts, ", "))
			}
			fmt.Fprintf(&sb, " = %q\n", fd.Value)
		}
	}
	return sb.String()
}

func orDash(s string) string {
	if s == "" {
		return "(no words)"
	}
	return s
}

func reply(w http.ResponseWriter, id json.RawMessage, result any, e *rpcError) {
	msg := map[string]any{"jsonrpc": "2.0"}
	if id != nil {
		msg["id"] = id
	} else {
		msg["id"] = nil
	}
	if e != nil {
		msg["error"] = e
	} else {
		msg["result"] = result
	}
	if w.Header().Get("Content-Type") == "" {
		w.Header().Set("Content-Type", "application/json")
	}
	json.NewEncoder(w).Encode(msg)
}
