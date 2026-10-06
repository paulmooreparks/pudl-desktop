// Copyright 2026 Paul Parks. SPDX-License-Identifier: Apache-2.0

// Package render draws pages in a headless Chromium browser, so that an
// agent can see what a person sees: PUDL Studio's canvas as a picture.
// One browser serves the whole service, started the first time a picture
// is asked for, and each picture is drawn in a tab of its own.
package render

import (
	"context"
	"errors"
	"os/exec"
	"sync"
	"time"

	"github.com/chromedp/chromedp"
)

// ErrNoBrowser is returned where the service has no browser to draw with.
var ErrNoBrowser = errors.New("this desktop has no browser to draw pictures with")

// Renderer draws pages. Path is the browser to run; empty finds one.
type Renderer struct {
	Path string

	mu      sync.Mutex
	browser context.Context
	stop    context.CancelFunc
}

// Find returns the first Chromium browser found among the usual places.
func Find() string {
	for _, name := range []string{"chromium-browser", "chromium", "google-chrome", "chrome",
		`C:\Program Files\Google\Chrome\Application\chrome.exe`,
		`C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe`} {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	return ""
}

func (r *Renderer) start() (context.Context, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.browser != nil && r.browser.Err() == nil {
		return r.browser, nil
	}
	path := r.Path
	if path == "" {
		path = Find()
	}
	if path == "" {
		return nil, ErrNoBrowser
	}
	opts := append(chromedp.DefaultExecAllocatorOptions[:], chromedp.ExecPath(path),
		chromedp.Flag("no-sandbox", true), chromedp.Flag("disable-dev-shm-usage", true))
	alloc, cancelAlloc := chromedp.NewExecAllocator(context.Background(), opts...)
	browser, cancel := chromedp.NewContext(alloc)
	if err := chromedp.Do(browser); err != nil {
		cancel()
		cancelAlloc()
		return nil, err
	}
	r.browser = browser
	r.stop = func() { cancel(); cancelAlloc() }
	return browser, nil
}

// Picture draws the page at url at a width, and returns it as a PNG of its
// whole height.
func (r *Renderer) Picture(ctx context.Context, url string, width int) ([]byte, error) {
	browser, err := r.start()
	if err != nil {
		return nil, err
	}
	tab, cancel := chromedp.NewContext(browser)
	defer cancel()
	tab, cancelTime := context.WithTimeout(tab, 20*time.Second)
	defer cancelTime()
	stopOnCancel := context.AfterFunc(ctx, cancel)
	defer stopOnCancel()
	if err := chromedp.Do(tab,
		chromedp.EmulateViewport(int64(width), 600),
		chromedp.Navigate(url),
		chromedp.WaitReady(chromedp.CSS("body")),
	); err != nil {
		return nil, err
	}
	// Fonts and PUDL's scripts settle before the picture is taken.
	if _, err := chromedp.Run(tab, chromedp.Evaluate[bool](`document.fonts.ready.then(() => new Promise(r => setTimeout(() => r(true), 100)))`, chromedp.EvalAwaitPromise)); err != nil {
		return nil, err
	}
	return chromedp.Run(tab, chromedp.FullScreenshot(100))
}

// Close stops the browser.
func (r *Renderer) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stop != nil {
		r.stop()
		r.browser, r.stop = nil, nil
	}
}
