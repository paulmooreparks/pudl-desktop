# PUDL Desktop

PUDL Desktop is a desktop of windows in the browser, built with [PUDL](https://github.com/paulmooreparks/pudl), over a small Go service that keeps files, revisions, drafts and jobs as addressable resources. It is meant to be useful for real work, it is the place where Markdown-first writing gets shown at its best, and it is the harness that exercises every part of PUDL against the conformance checklists in the [PUDL specification](https://github.com/paulmooreparks/pudl-spec).

Nothing is built yet. The project is in design.

- [The proposal](docs/proposal.md) sets out what the desktop is for, its architecture, the applications it starts with, and the decisions made on 4 October 2026, which come first in it.
- [The walk-through](docs/walkthrough.md) takes one Markdown file through creation, editing, a conflict, export and deletion, in the browser and in the terminal, before any code is written. It is the first design task the proposal calls for.

## Applet packages

The desktop installs [PUDL applet packages](https://github.com/paulmooreparks/pudl-applet-packages): copy a package's folder, or a collection's `dist/`, into the folder `-applets` names (`applets` beside the binary by default). Each package's `applet.json` gives the desktop its script, its page at `/app/<name>`, its window `app-<name>` with the least size it asks for, and its place in the Applets menu with its icon. The desktop gives packages the host kit's menu helpers through `window.pudlHost`; it has no file service, settings files or snapshots for them yet, so a package that wants those does without. [PUDLSplash](https://github.com/paulmooreparks/PUDLSplash), the arcade games, is the first collection.

## Licence

PUDL Desktop is released under the Apache License 2.0. See `LICENSE`.
