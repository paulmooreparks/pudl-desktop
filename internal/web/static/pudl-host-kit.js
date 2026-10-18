/* Copyright 2026 Paul Parks. SPDX-License-Identifier: Apache-2.0 */
/* pudl-host-kit 0.2.0: what every host gives a packaged applet, written
   once (README.md, "The host interface"). A host loads this before its own
   applet registry and builds window.pudlHost from it:

     window.pudlHost = pudlHostKit.host({ snapshot: ..., fs: ..., config: ..., pages: '/page/',
                                          services: { 'barcode-products': '/api/barcode-products/' } });

   An applet reads pudlHost and uses what is there; a service that is
   missing is a service the host does not have, and the applet does without.

     pudlHost.identity(root, entries)   the applet's first menu title: its own
                                        entries, then the window's commands
                                        (open as a page, copy the link,
                                        close) where it is in a window
     pudlHost.action(root, selector, label)
                                        a menu item that presses a button in
                                        the applet, disabled as the button is
     pudlHost.pages                     where applet pages are, such as /page/
     pudlHost.snapshot                  join(root, take) and leave, state kept
                                        across a reload (optional)
     pudlHost.fs                        readText(path), writeText(path, text)
                                        and mkdir(path) under ~ (optional),
                                        and, where the host has dialogs for
                                        them, open(options) and save(options)
                                        (each optional within fs) and
                                        onChange(fn) (below)
     pudlHost.config                    load, get, set, onChange for the
                                        applet's settings (optional)
     pudlHost.service(name)             the address of a service the host
                                        offers under that name, ending in /,
                                        or null where it offers none

   The file dialogs are the host's own, so files come from and go to
   wherever the host keeps them, and the reader's computer where it offers
   that:

     fs.open({ title, accept: ['.json'], start: '~', host: root,
               check: function (path) { return reason or null; } })
       resolves to { path, name, text } for a file of the host's, { name,
       text } for one from the reader's computer, or null if cancelled.
     fs.save({ title, name, blob, start: '~', host: root,
               check: function (path) { return reason or null; } })
       writes blob where the reader chooses and resolves to { path }, to
       { download: name } when the reader downloaded it instead, or to null
       if cancelled; it rejects with the reason a write failed. A host that
       keeps only text offers a picture as a download.
     fs.onChange(fn)
       calls fn() after any file of the host's changes, here or in another
       tab, and returns a function that stops it.

   A page with no server installs a package itself:

     pudlHostKit.install('/applets/hello/applet.json').then(function (manifest) { ... });

   fetches the manifest and defines the applet from it; the page then needs
   only a mount, <div data-applet="hello"></div>. */
(function () {
    'use strict';
    if (window.pudlHostKit) { return; }

    /* The applet's first menu: its own entries, then, in a window, the
       window's commands for the applet in the words an applet uses. Close
       window is not a danger here: closing loses nothing. On a page, Copy
       link to this applet copies the page's address. */
    function identity(root, entries) {
        var result = (entries || []).slice(), win = root.closest('[data-win]');
        if (win && window.pudlWindows) {
            var commands = window.pudlWindows.menuCommands(win.getAttribute('data-win'));
            ['page', 'copy-link', 'close'].forEach(function (id) {
                var command = commands.find(function (c) { return c.id === id; });
                if (!command) { return; }
                if (result.length && (id === 'page' || id === 'close')) { result.push('-'); }
                result.push(Object.assign({}, command, {
                    label: id === 'page' ? 'Open as a page' : id === 'copy-link' ? 'Copy link to this applet' : 'Close window'
                }, id === 'close' ? { danger: false } : {}));
            });
        } else if (navigator.clipboard) {
            if (result.length) { result.push('-'); }
            result.push({ label: 'Copy link to this applet', run: function () {
                return navigator.clipboard.writeText(location.href).catch(function () {
                    window.alert('The link could not be copied. Copy the address from your browser.');
                });
            } });
        }
        return result;
    }

    /* A control in the applet and its menu item share one availability and one action. */
    function action(root, selector, label) {
        var button = root.querySelector(selector);
        return { label: label, disabled: !button || button.disabled, run: function () {
            if (root.isConnected && button && !button.disabled) { button.click(); }
        } };
    }

    /* services, the named addresses, becomes service(name); the rest are
       pudlHost's members as given. */
    function host(given) {
        var named = (given && given.services) || {};
        var h = { identity: identity, action: action, pages: '/page/', service: function (name) {
            return Object.prototype.hasOwnProperty.call(named, name) ? named[name] : null;
        } };
        Object.keys(given || {}).forEach(function (k) { if (k !== 'services' && given[k] != null) { h[k] = given[k]; } });
        return h;
    }

    /* The define() a manifest stands for: its files at the manifest's own
       address, its page where the host keeps applet pages, and its version
       as the cache stamp. */
    function defineFrom(manifest, manifestUrl) {
        var base = String(manifestUrl).replace(/[^/]*$/, '');
        var pages = (window.pudlHost && window.pudlHost.pages) || '/page/';
        var cfg = { src: base + manifest.src, page: pages + manifest.name, ver: String(manifest.version) };
        if (manifest.css) { cfg.css = base + manifest.css; }
        if (manifest.handles) { cfg.handles = manifest.handles; }
        if (manifest.instances) { cfg.instances = manifest.instances; }
        if (manifest.actions) { cfg.actions = base + manifest.actions; }
        return cfg;
    }

    function install(manifestUrl) {
        return fetch(manifestUrl, { credentials: 'same-origin' })
            .then(function (r) { if (!r.ok) { throw new Error(manifestUrl + ': HTTP ' + r.status); } return r.json(); })
            .then(function (m) {
                if (!m || typeof m.name !== 'string' || typeof m.src !== 'string') { throw new Error(manifestUrl + ' is not an applet manifest'); }
                window.pudlApplets.define(m.name, defineFrom(m, manifestUrl));
                return m;
            });
    }

    window.pudlHostKit = { version: '0.2.0', identity: identity, action: action, host: host, install: install, defineFrom: defineFrom };
})();
