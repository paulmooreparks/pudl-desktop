/* Copyright 2026 Paul Parks. SPDX-License-Identifier: Apache-2.0

   PUDL Desktop's Windowed view. PUDL's windows script opens, places and
   addresses the windows; this script makes a form inside a window work in
   that window, as a form in a browser tab works in its tab.

   The form is sent exactly as it would be without script, to its own
   action, with one header more, X-PUDL-Window, naming the window. The
   server then answers with its result as a window rather than as a page.
   A result about the same resource as the window, such as a form refused
   with its errors marked, takes the window's place in it; a result about
   another resource, such as the document a Create made, opens in the
   window's place, as following a link in a tab does. Anything that is not
   a window, or a failure to reach the server, falls back to sending the
   form as a page, so nothing is lost. */
(function () {
  'use strict';

  function swapInto(win, fresh) {
    var body = win.querySelector(':scope > .win-body');
    var next = fresh.querySelector(':scope > .win-body');
    if (body && next) body.replaceWith(document.importNode(next, true));
    var title = fresh.querySelector('.win-title');
    if (title && window.pudlWindows) window.pudlWindows.retitle(win.getAttribute('data-win'), title.textContent.trim());
  }

  /* A window showing a file says where its latest revision number is, in
     data-watch, and the revision it shows, in data-revision. While it is
     showing, it checks every two seconds, and when the file has moved on,
     because an agent or another window changed it, it takes its content
     afresh from its own address. An editor does not watch, so nothing is
     taken from under a writer. */
  var WATCH_MS = 2000;
  function refresh(win) {
    var layer = document.querySelector('[data-win-layer]');
    var src = layer && layer.getAttribute('data-win-src');
    if (!src) return Promise.resolve();
    var key = win.getAttribute('data-win');
    return fetch(src.split('{key}').join(encodeURIComponent(key)), { credentials: 'same-origin' })
      .then(function (r) { return r.ok ? r.text() : null; })
      .then(function (text) {
        if (!text || !win.isConnected) return;
        var fresh = new DOMParser().parseFromString(text, 'text/html').querySelector('section.win[data-win]');
        if (!fresh) return;
        swapInto(win, fresh);
        win.setAttribute('data-revision', fresh.getAttribute('data-revision') || '');
      });
  }
  function watch() {
    document.querySelectorAll('.win[data-watch]').forEach(function (win) {
      if (win.hidden || win.hasAttribute('aria-busy') || win.__checking) return;
      win.__checking = true;
      fetch(win.getAttribute('data-watch'), { credentials: 'same-origin', cache: 'no-store' })
        .then(function (r) { return r.ok ? r.text() : null; })
        .then(function (rev) {
          if (rev !== null && rev.trim() !== win.getAttribute('data-revision')) return refresh(win);
        })
        .catch(function () {})
        .then(function () { win.__checking = false; });
    });
  }
  setInterval(function () { if (!document.hidden) watch(); }, WATCH_MS);

  document.addEventListener('submit', function (e) {
    var form = e.target;
    var win = form.closest && form.closest('.win[data-win]');
    if (!win || e.defaultPrevented || !window.fetch || !window.pudlWindows) return;
    var key = win.getAttribute('data-win');
    var method = (form.getAttribute('method') || 'get').toUpperCase();
    var fields = new URLSearchParams(new FormData(form, e.submitter || undefined));
    var url = form.action;
    var init = { method: method, credentials: 'same-origin', headers: { 'X-PUDL-Window': key, Accept: 'text/html' } };
    if (method === 'POST') init.body = fields;
    else url += (url.indexOf('?') < 0 ? '?' : '&') + fields.toString();
    e.preventDefault();
    win.setAttribute('aria-busy', 'true');
    fetch(url, init).then(function (r) {
      return r.text().then(function (text) { return { r: r, text: text }; });
    }).then(function (got) {
      win.removeAttribute('aria-busy');
      var doc = new DOMParser().parseFromString(got.text, 'text/html');
      var fresh = doc.querySelector('section.win[data-win]');
      if (!fresh) { location.assign(got.r.url); return; }
      var next = fresh.getAttribute('data-win');
      if (next === key) { swapInto(win, fresh); return; }
      /* The result's window may be open already, such as a document's
         after its editor saved it; it takes the fresh content, and the
         window the form was in gives way to it. */
      var open = document.querySelector('.win[data-win="' + CSS.escape(next) + '"]');
      if (open) swapInto(open, fresh);
      window.pudlWindows.replace(key, next);
    }).catch(function () {
      win.removeAttribute('aria-busy');
      form.submit();
    });
  });
})();
