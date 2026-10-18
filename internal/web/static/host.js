/* Copyright 2026 Paul Parks. SPDX-License-Identifier: Apache-2.0 */
/* What the desktop gives an installed applet package (pudl-host-kit.js,
   from the pudl-applet-packages repo): the kit's menu helpers and where
   applet pages are. The desktop has no file service, settings files or
   snapshots for applets yet, so an applet that wants them does without;
   the games keep their scores in this browser. Loads before the packages'
   registry (/applets/registry.js). */
(function () {
    'use strict';
    if (!window.pudlHostKit) { return; }
    window.pudlHost = window.pudlHostKit.host({ pages: '/app/' });
})();
