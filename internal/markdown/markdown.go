// Copyright 2026 Paul Parks. SPDX-License-Identifier: Apache-2.0

// Package markdown renders PUDL Desktop's documents. It is the one
// renderer every view uses (docs/walkthrough.md, decision 5): CommonMark
// with GitHub's tables, task lists, strikethrough and autolinks, and YAML
// front matter read as metadata and left out of the rendered body.
//
// Raw HTML written in a document is not passed through. goldmark leaves
// it out unless told otherwise, and it stays out until there is a
// sanitising policy for it, since a rendered document is shown inside the
// desktop's own pages.
//
// The output uses PUDL's components where PUDL has one for the element: a
// table is a data-table and a code block is a pre.code. PUDL has no
// component for running prose yet, so the rest is plain HTML.
package markdown

import (
	"bytes"

	"github.com/yuin/goldmark"
	meta "github.com/yuin/goldmark-meta"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	east "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

var md = goldmark.New(
	goldmark.WithExtensions(
		extension.Table,
		extension.TaskList,
		extension.Strikethrough,
		extension.Linkify,
		meta.Meta,
	),
	goldmark.WithParserOptions(
		parser.WithAutoHeadingID(),
		parser.WithASTTransformers(util.Prioritized(pudlClasses{}, 100)),
	),
	goldmark.WithRendererOptions(
		renderer.WithNodeRenderers(util.Prioritized(codeRenderer{}, 100)),
	),
)

// pudlClasses gives a table PUDL's data-table class.
type pudlClasses struct{}

func (pudlClasses) Transform(doc *ast.Document, _ text.Reader, _ parser.Context) {
	ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if entering && n.Kind() == east.KindTable {
			n.SetAttributeString("class", []byte("data-table"))
		}
		return ast.WalkContinue, nil
	})
}

// codeRenderer writes a code block as PUDL's pre.code, keeping the fenced
// block's language as the code element's language-* class, which is the
// convention highlighters read.
type codeRenderer struct{}

func (codeRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(ast.KindCodeBlock, renderCode)
	reg.Register(ast.KindFencedCodeBlock, renderCode)
}

func renderCode(w util.BufWriter, src []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	w.WriteString(`<pre class="code" tabindex="0"><code`)
	if f, ok := n.(*ast.FencedCodeBlock); ok {
		if lang := f.Language(src); lang != nil {
			w.WriteString(` class="language-`)
			html.DefaultWriter.Write(w, lang)
			w.WriteString(`"`)
		}
	}
	w.WriteString(">")
	lines := n.Lines()
	for i := 0; i < lines.Len(); i++ {
		seg := lines.At(i)
		html.DefaultWriter.RawWrite(w, seg.Value(src))
	}
	w.WriteString("</code></pre>\n")
	return ast.WalkSkipChildren, nil
}

// Document is a rendered Markdown document.
type Document struct {
	HTML []byte
	Meta map[string]any
}

// Render renders Markdown source.
func Render(src []byte) (*Document, error) {
	var buf bytes.Buffer
	ctx := parser.NewContext()
	if err := md.Convert(src, &buf, parser.WithContext(ctx)); err != nil {
		return nil, err
	}
	m, err := meta.TryGet(ctx)
	if err != nil {
		// Front matter that is not valid YAML is shown as a problem rather
		// than failing the whole document.
		m = map[string]any{"error": err.Error()}
	}
	return &Document{HTML: buf.Bytes(), Meta: m}, nil
}
