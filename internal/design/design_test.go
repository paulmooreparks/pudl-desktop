// Copyright 2026 Paul Parks. SPDX-License-Identifier: Apache-2.0

package design

import (
	"strings"
	"testing"
)

const applet = `<div class="settings-panel">
  <section class="card"><h2 class="card-title">Reading</h2>
    <label class="check"><input type="checkbox"> Open in a window</label>
  </section>
</div>
`

func parse(t *testing.T, src string) *Doc {
	t.Helper()
	d, err := Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestRoundTrip(t *testing.T) {
	d := parse(t, applet)
	if got := string(d.Render()); got != applet {
		t.Fatalf("a document read and written back should be unchanged:\n%s", got)
	}
}

func TestTreeAndPaths(t *testing.T) {
	d := parse(t, applet)
	tree, _ := d.Tree("")
	if len(tree) != 1 || Describe(tree[0]) != "div.settings-panel" || tree[0].Children[0].Path != "0.0" {
		t.Fatalf("tree: %+v", tree)
	}
	title, err := d.Element("0.0.0")
	if err != nil || title.Tag != "h2" || title.Text != "Reading" {
		t.Fatalf("the title should be at 0.0.0: %+v %v", title, err)
	}
	if _, err := d.Element("0.9"); err != ErrPath {
		t.Fatalf("a path to nothing should be refused: %v", err)
	}
}

func TestOperations(t *testing.T) {
	d := parse(t, applet)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(d.SetText("0.0.0", "Reading & windows"))
	must(d.SetAttr("0.0", "aria-label", "Reading"))
	at, err := d.Insert("0.0", "last", []byte(`<div class="settings-actions"><button class="btn btn-primary">Save</button></div>`))
	must(err)
	if at != "0.0.2" {
		t.Fatalf("the inserted element's path: %s", at)
	}
	moved, err := d.Move("0.0.2", "0.0", "before")
	must(err)
	if moved != "0.0" {
		t.Fatalf("the moved element's path: %s", moved)
	}
	must(d.RemoveAttr("0.1", "aria-label"))
	out := string(d.Render())
	for _, s := range []string{`<h2 class="card-title">Reading &amp; windows</h2>`, `<div class="settings-actions"><button class="btn btn-primary">Save</button></div><section class="card">`} {
		if !strings.Contains(out, s) {
			t.Fatalf("want %s in\n%s", s, out)
		}
	}
	if strings.Contains(out, "aria-label") {
		t.Fatalf("the attribute should be gone:\n%s", out)
	}
	if _, err := d.Move("0", "0.0", "last"); err != ErrInside {
		t.Fatalf("moving an element into itself should be refused: %v", err)
	}
	must(d.Remove("0.0"))
	if strings.Contains(string(d.Render()), "settings-actions") {
		t.Fatal("the removed element should be gone")
	}
	if err := d.SetAttr("0", "on load", "x"); err != ErrName {
		t.Fatalf("a bad attribute name should be refused: %v", err)
	}
}
