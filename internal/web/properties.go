// Copyright 2026 Paul Parks. SPDX-License-Identifier: Apache-2.0

package web

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/paulmooreparks/pudl-desktop/internal/design"
)

// === Properties ============================================================
//
// An element's properties are the variants and states of the PUDL
// components it is, offered as one form. Which components an element is
// comes from PUDL's component list: the element matches the root of a
// component's template, and every part the component lists is the element
// or inside it. A grid is a data table too, so an element can be more than
// one component, and each has its own form.
//
// The form's fields are named for what they set, so that a client sending
// a map of names to values, as an agent does, can set any of them:
// "variant:<class>" for a variant on its own, "group:<name>" for a choice
// among alternatives, and "state:<n>" for the component's nth state. A
// checkbox left out is off.

type propChoice struct {
	Label, Value string
	Checked      bool
}

type propVariantGroup struct {
	Name    string
	Field   string
	Choices []propChoice
}

type propField struct {
	Label, Field, Help string
	Checked            bool
}

type propForm struct {
	Component component
	Groups    []propVariantGroup
	Variants  []propField
	States    []propField
}

// rootSelector is a selector for the root element of a component's
// template: its tag and its classes.
func rootSelector(c component) string {
	d, err := design.Parse([]byte(c.Template))
	if err != nil {
		return ""
	}
	tree, _ := d.Tree("")
	if len(tree) == 0 {
		return ""
	}
	root := tree[0]
	sel := root.Tag
	for _, a := range root.Attrs {
		if a.Key == "class" {
			for _, cl := range strings.Fields(a.Val) {
				sel += "." + cl
			}
		}
	}
	return sel
}

// componentsOf lists the components the element at path is.
func componentsOf(d *design.Doc, path string) []component {
	loadPalette()
	var out []component
	for _, c := range palette.Components {
		if len(c.Variants) == 0 && len(c.States) == 0 {
			continue
		}
		root := rootSelector(c)
		if root == "" {
			continue
		}
		if ok, err := d.Matches(path, root); err != nil || !ok {
			continue
		}
		fits := true
		for _, p := range c.Parts {
			if found, err := d.Find(path, p.Selector); err != nil || found == "" {
				fits = false
				break
			}
		}
		if fits {
			out = append(out, c)
		}
	}
	return out
}

// target is the element a variant or state applies to: the element
// itself, or the first inside it that matches its "on" selector.
func target(d *design.Doc, path, on string) (string, bool) {
	if on == "" {
		return path, true
	}
	p, err := d.Find(path, on)
	return p, err == nil && p != ""
}

func stateOn(d *design.Doc, at string, s state) bool {
	v, has := d.Attr(at, s.Attribute)
	if !has {
		return false
	}
	if s.Value == "" {
		return true
	}
	return v == s.Value
}

func propForms(d *design.Doc, path string) []propForm {
	var out []propForm
	for _, c := range componentsOf(d, path) {
		f := propForm{Component: c}
		groups := map[string]int{}
		for _, v := range c.Variants {
			at, ok := target(d, path, v.On)
			if !ok {
				continue
			}
			on := d.HasClass(at, v.Class)
			if v.Group == "" {
				f.Variants = append(f.Variants, propField{Label: v.Name, Field: "variant:" + v.Class, Checked: on})
				continue
			}
			i, seen := groups[v.Group]
			if !seen {
				i = len(f.Groups)
				groups[v.Group] = i
				f.Groups = append(f.Groups, propVariantGroup{Name: v.Group, Field: "group:" + v.Group,
					Choices: []propChoice{{Label: "Plain", Value: "", Checked: true}}})
			}
			g := &f.Groups[i]
			g.Choices = append(g.Choices, propChoice{Label: v.Name, Value: v.Class, Checked: on})
			if on {
				g.Choices[0].Checked = false
			}
		}
		for i, s := range c.States {
			at, ok := target(d, path, s.On)
			if !ok {
				continue
			}
			help := s.Attribute
			if s.Value != "" {
				help += `="` + s.Value + `"`
			}
			f.States = append(f.States, propField{Label: s.Name, Field: fmt.Sprintf("state:%d", i), Help: help, Checked: stateOn(d, at, s)})
		}
		out = append(out, f)
	}
	return out
}

// setProperties applies a properties form: every variant and state of the
// component is set as the form says, so that what is left out is off.
// Only what changes is written, so a state that is off and was never there,
// such as aria-expanded on a button that discloses nothing, stays absent.
func setProperties(d *design.Doc, path string, form url.Values) error {
	loadPalette()
	id := form.Get("component")
	var c *component
	for _, have := range componentsOf(d, path) {
		if have.ID == id {
			c = &have
			break
		}
	}
	if c == nil {
		return fmt.Errorf("the element is not a %q component", id)
	}
	for _, v := range c.Variants {
		at, ok := target(d, path, v.On)
		if !ok {
			continue
		}
		var on bool
		if v.Group == "" {
			on = form.Get("variant:"+v.Class) != ""
		} else {
			on = form.Get("group:"+v.Group) == v.Class
		}
		if on == d.HasClass(at, v.Class) {
			continue
		}
		if err := d.SetClass(at, v.Class, on); err != nil {
			return err
		}
		if v.Replaces != "" {
			if err := d.SetClass(at, v.Replaces, !on); err != nil {
				return err
			}
		}
	}
	for i, s := range c.States {
		at, ok := target(d, path, s.On)
		if !ok {
			continue
		}
		on := form.Get(fmt.Sprintf("state:%d", i)) != ""
		if on == stateOn(d, at, s) {
			continue
		}
		var err error
		switch {
		case on:
			err = d.SetAttr(at, s.Attribute, s.Value)
		case s.Off != "":
			err = d.SetAttr(at, s.Attribute, s.Off)
		default:
			err = d.RemoveAttr(at, s.Attribute)
		}
		if err != nil {
			return err
		}
	}
	return nil
}
