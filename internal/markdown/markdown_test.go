// Copyright 2026 Paul Parks. SPDX-License-Identifier: Apache-2.0

package markdown

import (
	"strings"
	"testing"
)

func render(t *testing.T, src string) string {
	t.Helper()
	d, err := Render([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	return string(d.HTML)
}

func TestPUDLComponents(t *testing.T) {
	out := render(t, "| A |\n|---|\n| 1 |\n\n```go\nx := 1 < 2\n```\n\n    indented\n")
	for _, s := range []string{
		`<table class="data-table">`,
		`<pre class="code" tabindex="0"><code class="language-go">x := 1 &lt; 2`,
		`<pre class="code" tabindex="0"><code>indented`,
	} {
		if !strings.Contains(out, s) {
			t.Errorf("want %s in\n%s", s, out)
		}
	}
}

func TestRawHTMLStaysOut(t *testing.T) {
	out := render(t, "<script>alert(1)</script>\n\nText <b onclick=x>bold</b>\n\n```\n</code><script>x</script>\n```\n")
	if strings.Contains(out, "<script") || strings.Contains(out, "<b ") {
		t.Fatalf("raw HTML should not pass through:\n%s", out)
	}
}

func TestFrontMatter(t *testing.T) {
	d, err := Render([]byte("---\ntitle: Trip\n---\n# Body\n"))
	if err != nil {
		t.Fatal(err)
	}
	if d.Meta["title"] != "Trip" || strings.Contains(string(d.HTML), "title:") {
		t.Fatalf("front matter should be metadata only: %v\n%s", d.Meta, d.HTML)
	}
}
