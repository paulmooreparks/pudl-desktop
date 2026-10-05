// Copyright 2026 Paul Parks. SPDX-License-Identifier: Apache-2.0

package web

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
)

// browser starts a headless Chromium browser for a test: the one
// PUDL_CHROME names, or the first of the usual ones found, Edge among
// them. Where there is none the test is skipped, and says so.
func browser(t *testing.T) context.Context {
	t.Helper()
	path := os.Getenv("PUDL_CHROME")
	if path == "" {
		for _, name := range []string{"chromium", "chromium-browser", "google-chrome", "chrome",
			`C:\Program Files\Google\Chrome\Application\chrome.exe`,
			`C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe`} {
			if p, err := exec.LookPath(name); err == nil {
				path = p
				break
			}
		}
	}
	if path == "" {
		t.Skip("no Chromium browser to drive; set PUDL_CHROME to one")
	}
	opts := append(chromedp.DefaultExecAllocatorOptions[:], chromedp.WindowSize(1280, 800), chromedp.ExecPath(path))
	alloc, cancelAlloc := chromedp.NewExecAllocator(context.Background(), opts...)
	ctx, cancel := chromedp.NewContext(alloc)
	ctx, cancelTime := context.WithTimeout(ctx, 60*time.Second)
	t.Cleanup(func() { cancelTime(); cancel(); cancelAlloc() })
	return ctx
}

// js runs a script in the page and returns its string result; an async
// script's promise is awaited.
func js(ctx context.Context, expr string) (string, error) {
	return chromedp.Run(ctx, chromedp.Evaluate[string](expr, chromedp.EvalAwaitPromise))
}

func wait(ctx context.Context, cond string) error {
	for i := 0; i < 100; i++ {
		got, err := js(ctx, `String(!!(`+cond+`))`)
		if err != nil {
			return err
		}
		if got == "true" {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return errors.New("timed out waiting for " + cond)
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// TestWindowedView follows the walkthrough in the Windowed view: the
// desktop opens on the workspace's window, a form in a window works in
// that window, saving refreshes the document's window, and a conflict
// comes back into the editor's window with the writer's text kept.
func TestWindowedView(t *testing.T) {
	hs := newDesktop(t)
	ctx := browser(t)
	must(t, chromedp.Do(ctx,
		chromedp.Navigate(hs.URL+"/"),
		chromedp.Click(chromedp.CSS(`form[action="/workspaces"] button`)),
		chromedp.WaitVisible(chromedp.CSS(`.win[data-win^="r-"] form[action$="/entries"]`)),
	))
	titles, err := js(ctx, `[...document.querySelectorAll('.win .win-title')].map(t => t.textContent).join('|')`)
	must(t, err)
	if titles != "Workspace" {
		t.Fatalf("the desktop should open on the workspace's window, got %q", titles)
	}

	// Creating a document in the folder's window shows it in that window's place.
	_, err = js(ctx, `(() => {
		const f = document.querySelector('.win form[action$="/entries"]');
		f.querySelector('input[name=name]').value = 'report.md';
		f.requestSubmit();
		return '';
	})()`)
	must(t, err)
	must(t, wait(ctx, `[...document.querySelectorAll('.win .win-title')].map(t => t.textContent).join('|') === 'report.md'`))

	// Edit opens the editor's window; saving there brings the document's
	// window forward with the new revision, and the editor gives way.
	must(t, chromedp.Do(ctx, chromedp.Click(chromedp.CSS(`.win a[rel="edit-form"]`))))
	must(t, wait(ctx, `document.querySelector('.win[data-win^="edit-"] textarea')`))
	_, err = js(ctx, `(() => {
		const ed = document.querySelector('.win[data-win^="edit-"]');
		ed.querySelector('textarea').value = '# Report\n\nWritten in a window.\n';
		ed.querySelector('form').requestSubmit();
		return '';
	})()`)
	must(t, err)
	if err := wait(ctx, `!document.querySelector('.win[data-win^="edit-"]') && /^2,/.test((document.querySelector('.win[data-win^="r-"] .kv-table td') || {}).textContent || '')`); err != nil {
		state, _ := js(ctx, `[...document.querySelectorAll('.win')].map(w => w.getAttribute('data-win') + ':' + ((w.querySelector('.kv-table td') || {}).textContent || '')).join('|')`)
		t.Fatalf("saving should refresh the document's window at revision 2: %v (%s)", err, state)
	}

	// A save against a stale revision comes back into the editor's window
	// as the conflict, with the writer's text kept.
	must(t, chromedp.Do(ctx, chromedp.Click(chromedp.CSS(`.win a[rel="edit-form"]`))))
	must(t, wait(ctx, `document.querySelector('.win[data-win^="edit-"] textarea')`))
	_, err = js(ctx, `(async () => {
		const ed = document.querySelector('.win[data-win^="edit-"]');
		const form = ed.querySelector('form');
		const other = new URLSearchParams(new FormData(form));
		other.set('submission', 'elsewhere'); other.set('text', 'Saved elsewhere.\n');
		await fetch(form.action, { method: 'POST', body: other });
		ed.querySelector('textarea').value = 'Mine.\n';
		form.requestSubmit();
		return '';
	})()`)
	must(t, err)
	must(t, wait(ctx, `/changed while you were editing/.test((document.querySelector('.win[data-win^="edit-"]') || {}).textContent || '')`))
	mine, err := js(ctx, `document.querySelector('.win[data-win^="edit-"] textarea').value`)
	must(t, err)
	if !strings.Contains(mine, "Mine.") {
		t.Fatalf("the conflict should keep the writer's text in the window, got %q", mine)
	}
}
