// Copyright 2026 Paul Parks. SPDX-License-Identifier: Apache-2.0

package web

import (
	"strings"
	"testing"
)

// TestAgentSetsProperties: an agent inserts a button and a text field and
// sets their variants and states through the properties form, then turns
// them off again; the source follows.
func TestAgentSetsProperties(t *testing.T) {
	hs := newDesktop(t)
	a := newAgent(t, hs.URL)
	home := a.send(a.open("/"), RelBase+"start", nil)
	file := a.send(home, RelBase+"create", map[string]string{"name": "applet.html", "kind": "file"})
	design := a.open(a.link(file, RelBase+"design"))
	raw := strings.TrimSuffix(a.link(file, RelBase+"design"), "/design") + "/raw"
	source := func() string { return a.open(raw).Content[0].Text }

	button := a.send(design, RelBase+"insert", map[string]string{"component": "button", "position": "last"})
	if !strings.Contains(button.Content[0].Text, "Button properties") {
		t.Fatalf("a button's page should offer its properties:\n%s", button.Content[0].Text)
	}
	a.send(button, RelBase+"properties", map[string]string{"group:kind": "btn-primary", "variant:btn-sm": "on", "state:0": "on"})
	if s := source(); !strings.Contains(s, `<button type="button" class="btn btn-primary btn-sm" disabled>Button</button>`) {
		t.Fatalf("the button should be primary, small and disabled:\n%s", s)
	}

	// The page read afresh shows the properties as set; sending it with
	// the kind plain and the small variant and the state off undoes them.
	button = a.open(button.Structured.Page.URL)
	a.send(button, RelBase+"properties", map[string]string{"group:kind": "", "variant:btn-sm": "", "state:0": ""})
	if s := source(); !strings.Contains(s, `<button type="button" class="btn">Button</button>`) {
		t.Fatalf("the button should be plain again:\n%s", s)
	}

	// A text field's states apply to its input, inside it.
	design = a.open(a.link(file, RelBase+"design"))
	field := a.send(design, RelBase+"insert", map[string]string{"component": "text-field", "position": "last"})
	if !strings.Contains(field.Content[0].Text, "Text field properties") {
		t.Fatalf("a text field's page should offer its properties:\n%s", field.Content[0].Text)
	}
	a.send(field, RelBase+"properties", map[string]string{"state:0": "on", "state:1": "on"})
	s := source()
	if !strings.Contains(s, `class="form-input"`) || !strings.Contains(s, ` required`) || !strings.Contains(s, `aria-invalid="true"`) {
		t.Fatalf("the field's input should be required and invalid:\n%s", s)
	}
	if strings.Contains(s, `<div class="form-group" required`) {
		t.Fatalf("the states belong on the input, not the group:\n%s", s)
	}
}
