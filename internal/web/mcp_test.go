// Copyright 2026 Paul Parks. SPDX-License-Identifier: Apache-2.0

package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/paulmooreparks/pudl-desktop/internal/hyper"
)

// agent is an MCP client, as Claude would be: it knows the protocol and
// the two tools, and finds everything else in the pages it reads.
type agent struct {
	t       *testing.T
	url     string
	session string
	bearer  string
	id      int
}

type result struct {
	Content []struct {
		Text string `json:"text"`
	} `json:"content"`
	Structured struct {
		Status int        `json:"status"`
		Page   hyper.Page `json:"page"`
	} `json:"structuredContent"`
	IsError bool `json:"isError"`
}

func (a *agent) rpc(method string, params any) json.RawMessage {
	a.t.Helper()
	a.id++
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": a.id, "method": method, "params": params})
	req, _ := http.NewRequest("POST", a.url, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if a.session != "" {
		req.Header.Set("Mcp-Session-Id", a.session)
	}
	if a.bearer != "" {
		req.Header.Set("Authorization", "Bearer "+a.bearer)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		a.t.Fatal(err)
	}
	defer resp.Body.Close()
	if s := resp.Header.Get("Mcp-Session-Id"); s != "" {
		a.session = s
	}
	var msg struct {
		Result json.RawMessage `json:"result"`
		Error  *struct{ Message string }
	}
	if err := json.NewDecoder(resp.Body).Decode(&msg); err != nil {
		a.t.Fatal(err)
	}
	if msg.Error != nil {
		a.t.Fatalf("%s: %s", method, msg.Error.Message)
	}
	return msg.Result
}

func (a *agent) tool(name string, args any) result {
	a.t.Helper()
	var r result
	if err := json.Unmarshal(a.rpc("tools/call", map[string]any{"name": name, "arguments": args}), &r); err != nil {
		a.t.Fatal(err)
	}
	if r.IsError {
		a.t.Fatalf("%s: %s", name, r.Content[0].Text)
	}
	return r
}

func (a *agent) open(url string) result { return a.tool("open", map[string]any{"url": url}) }

// send fills the page's form with the relation and sends it, as an agent
// following the instructions does: every field as the page gave it, but
// for the ones it changes.
func (a *agent) send(r result, rel string, changes map[string]string) result {
	a.t.Helper()
	for _, f := range r.Structured.Page.Forms {
		if f.Rel == rel {
			fields := map[string]string{}
			for k, v := range f.Values(changes) {
				fields[k] = v[0]
			}
			return a.tool("submit", map[string]any{"action": f.Action, "method": f.Method, "fields": fields})
		}
	}
	a.t.Fatalf("%s has no form rel=%q:\n%s", r.Structured.Page.URL, rel, r.Content[0].Text)
	return result{}
}

func (a *agent) link(r result, rel string) string {
	a.t.Helper()
	for _, l := range r.Structured.Page.Links {
		if strings.Contains(" "+l.Rel+" ", " "+rel+" ") {
			return l.Href
		}
	}
	a.t.Fatalf("%s has no link rel=%q", r.Structured.Page.URL, rel)
	return ""
}

func newAgent(t *testing.T, base string) *agent {
	a := &agent{t: t, url: base + "/mcp"}
	var init struct {
		ProtocolVersion string `json:"protocolVersion"`
		Instructions    string `json:"instructions"`
	}
	json.Unmarshal(a.rpc("initialize", map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{},
		"clientInfo": map[string]any{"name": "test", "version": "1"}}), &init)
	if init.ProtocolVersion != "2025-06-18" || a.session == "" || init.Instructions == "" {
		t.Fatalf("initialize should agree the version, give a session and instructions: %+v %q", init, a.session)
	}
	return a
}

// TestAgentWalkthrough is the walkthrough done by an agent through MCP,
// with only the open and submit tools.
func TestAgentWalkthrough(t *testing.T) {
	hs := newDesktop(t)
	a := newAgent(t, hs.URL)
	var list struct{ Tools []struct{ Name string } }
	json.Unmarshal(a.rpc("tools/list", map[string]any{}), &list)
	if len(list.Tools) != 2 || list.Tools[0].Name != "open" || list.Tools[1].Name != "submit" {
		t.Fatalf("the tools should be open and submit: %+v", list)
	}

	desk := a.send(a.open("/"), RelBase+"start", nil)
	if desk.Structured.Status != 200 || !strings.Contains(desk.Content[0].Text, "This folder is empty") {
		t.Fatalf("starting should land on the workspace with its folder:\n%s", desk.Content[0].Text)
	}
	file := a.send(desk, RelBase+"create", map[string]string{"name": "plan.md", "kind": "file"})
	if file.Structured.Page.Title != "plan.md" {
		t.Fatalf("creating should show the new document, got %q", file.Structured.Page.Title)
	}
	edit := a.open(a.link(file, "edit-form"))
	saved := a.send(edit, RelBase+"save", map[string]string{"text": "# Plan\n\nWritten by an agent.\n"})
	if !strings.Contains(saved.Content[0].Text, "Revision 2,") {
		t.Fatalf("saving should make revision 2: status %d %s\n%q", saved.Structured.Status, saved.Structured.Page.URL, saved.Content[0].Text)
	}
	var source string
	for _, l := range saved.Structured.Page.Links {
		if l.Rel == "alternate" && l.Type == "text/markdown" {
			source = l.Href
		}
	}
	src := a.open(source)
	if !strings.Contains(src.Content[0].Text, "Written by an agent.") {
		t.Fatalf("the source should be what the agent wrote:\n%s", src.Content[0].Text)
	}
}

// TestAgentDesigns: an agent builds an applet's markup from the palette
// through the design pages, and a stale path is refused.
func TestAgentDesigns(t *testing.T) {
	hs := newDesktop(t)
	a := newAgent(t, hs.URL)
	desk := a.send(a.open("/"), RelBase+"start", nil)
	file := a.send(desk, RelBase+"create", map[string]string{"name": "applet.html", "kind": "file"})
	designPage := a.open(a.link(file, RelBase+"design"))
	if !strings.Contains(designPage.Content[0].Text, "The document is empty.") {
		t.Fatalf("a new document's design should be empty:\n%s", designPage.Content[0].Text)
	}

	// A settings panel goes into the empty document, and the result is
	// the panel's own page.
	panel := a.send(designPage, RelBase+"insert", map[string]string{"component": "settings-panel", "position": "last"})
	if panel.Structured.Status != 200 || !strings.Contains(panel.Structured.Page.Title, "div.settings-panel") {
		t.Fatalf("inserting should show the new element:\n%s", panel.Content[0].Text)
	}
	// Its card's title gets new words, found by following the tree.
	var card, title string
	for _, l := range panel.Structured.Page.Links {
		if l.Rel == "item" && strings.Contains(l.Text, "section.card") {
			card = l.Href
		}
	}
	cardPage := a.open(card)
	for _, l := range cardPage.Structured.Page.Links {
		if l.Rel == "item" && strings.Contains(l.Text, "h2.card-title") {
			title = l.Href
		}
	}
	titlePage := a.open(title)
	a.send(titlePage, RelBase+"set-text", map[string]string{"text": "Reading"})
	cardPage = a.open(card)
	a.send(cardPage, RelBase+"set-attribute", map[string]string{"name": "aria-label", "value": "Reading settings"})

	// The source is plain HTML, with nothing of Studio's in it.
	src := a.open(strings.TrimSuffix(a.link(file, RelBase+"design"), "/design") + "/raw")
	for _, s := range []string{`<div class="settings-panel">`, `<section class="card" aria-label="Reading settings">`, `<h2 class="card-title">Reading</h2>`} {
		if !strings.Contains(src.Content[0].Text, s) {
			t.Fatalf("want %s in the source:\n%s", s, src.Content[0].Text)
		}
	}

	// A form read at an older revision is refused, and changes nothing.
	// (Sent again with its own submission token, it would be the same
	// change replayed, which the server answers as it did the first time.)
	stale := a.send(titlePage, RelBase+"set-text", map[string]string{"text": "Stale", "submission": "another-sending"})
	if stale.Structured.Status != http.StatusConflict {
		t.Fatalf("an operation at an old revision should be refused with 409, got %d", stale.Structured.Status)
	}
	src = a.open(strings.TrimSuffix(a.link(file, RelBase+"design"), "/design") + "/raw")
	if strings.Contains(src.Content[0].Text, "Stale") {
		t.Fatal("the refused operation should change nothing")
	}
}

// TestAgentInPersonsWorkspace: a person starts a workspace and gives an
// agent its token; the agent's change is there for the person.
func TestAgentInPersonsWorkspace(t *testing.T) {
	hs := newDesktop(t)
	person := newClient(t, hs.URL)
	desk := person.submit(person.get("/"), RelBase+"start", nil)
	agentPage := person.get(strings.TrimSuffix(strings.Split(desk.url, "?")[0], "/") + "/agent")
	want(t, agentPage, http.StatusOK)
	i := strings.Index(agentPage.body, "Authorization: Bearer ")
	if i < 0 {
		t.Fatalf("the agent page should give the bearer header:\n%s", agentPage.body)
	}
	token := strings.Fields(agentPage.body[i+len("Authorization: Bearer "):])[0]
	token = strings.TrimSuffix(strings.Split(token, "<")[0], "\"")

	a := newAgent(t, hs.URL)
	a.bearer = token
	ws := a.open(strings.TrimPrefix(desk.url, hs.URL))
	if ws.Structured.Status != 200 {
		t.Fatalf("the agent should reach the workspace with its token, got %d", ws.Structured.Status)
	}
	a.send(ws, RelBase+"create", map[string]string{"name": "from-agent.md"})
	again := person.get(desk.url)
	if !strings.Contains(again.body, "from-agent.md") {
		t.Fatal("the person should see the document the agent made")
	}

	stranger := newAgent(t, hs.URL)
	if r := stranger.open(strings.TrimPrefix(desk.url, hs.URL)); r.Structured.Status != 404 {
		t.Fatalf("an agent without the token should get 404, got %d", r.Structured.Status)
	}
}
