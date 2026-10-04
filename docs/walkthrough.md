# One Markdown file, from creation to deletion

This walk-through follows a single Markdown file through its whole life in PUDL Desktop: a workspace is started, the file is created and written, read in another application, caught in a conflict, exported, deleted and restored. Each step is shown twice, once through the browser's HTML links and forms and once through the terminal, because the proposal's test of the design is that both clients work from the same representations and arrive at the same result.

No code exists yet. Where this document makes a choice the proposal left open, it says so, and the choices that need Paul's decision are gathered at the end. The addresses in the examples show the shape of the design; clients find real addresses in representations and never build them.

## The rules the walk-through relies on

### The document format

A document is a Markdown file written in [CommonMark](https://spec.commonmark.org/0.31.2/), with four extensions from [GitHub Flavored Markdown](https://github.github.com/gfm/): tables, task lists, strikethrough and autolinks. A file may begin with YAML front matter between `---` lines, which the desktop reads as the document's metadata. A link from one document to another is an ordinary relative link to the other file's path, such as `[the budget](../data/budget.md)`, so a document reads correctly in any other Markdown tool.

The service renders Markdown to HTML with [goldmark](https://github.com/yuin/goldmark), a CommonMark-compliant Go library under the MIT licence, which supports the four extensions and front matter through its extension packages. Preview, export and the editor's live preview all use this one renderer, so a document never looks different in two places. A browser-side renderer could come later for speed, but only if it is shown to produce the same HTML from the same source.

### Addresses

Every file and folder has a stable identity, and two kinds of address.

- The path address, such as `/w/k7f3/files/notes/report.md`, follows the file's place in its folders. It is what a Markdown link resolves against and what a person reads in the address bar.
- The identity address, such as `/w/k7f3/r/01JB8Q`, never changes while the file exists. The desktop's window state uses it, so a window showing a file keeps showing it after the file is renamed or moved.

Each representation of a file includes both, the identity address as `rel="canonical"`. When a file is renamed, a request for its old path address is redirected to the new one for as long as the workspace lasts, so links into it from outside keep working.

### Hypermedia

HTML is the only representation format in stage 1. A representation offers what can be done next as links and forms, and leaves out what cannot be done, so a read-only file has no edit form and a finished job has no cancel form. Every link and form says what it is for in a `rel` attribute, which HTML allows on forms as well as links.

Relations come from the IANA registry wherever one fits: `edit-form` and `create-form` from [RFC 6861](https://www.rfc-editor.org/rfc/rfc6861), `version-history`, `latest-version`, `predecessor-version`, `working-copy` and `working-copy-of` from [RFC 5829](https://www.rfc-editor.org/rfc/rfc5829), and `collection`, `item`, `up`, `canonical` and `alternate`. A relation the registry does not have is an absolute URI under `https://pudl.parkscomputing.com/rel/`, such as `…/rel/export`, and each of those URIs serves a page that documents the relation, as [RFC 8288](https://www.rfc-editor.org/rfc/rfc8288) recommends for extension relations.

HTML forms can send only GET and POST, so in stage 1 every change is a POST to the address in a form's `action`. The terminal follows the same forms, so there is one way to make each change.

### The rules every change follows

- A form that changes a file includes a hidden `base` field with the revision the change was made against. If the file has moved on since, the service refuses the change with a conflict instead of overwriting.
- A form includes a hidden `submission` field with a token made when the form was rendered. The service remembers the outcome of each token, so a form sent twice, by a double click or a retry after a dropped connection, gets the first outcome again instead of a second change.
- A successful POST returns `303 See Other` and the address of the result, so reload and Back never send a form again.
- A form refused for invalid input comes back with the reader's input kept and each problem shown beside its field, as PUDL's form states draw it, with `422 Unprocessable Content`. A conflict comes back as `409 Conflict` with a page that resolves it.

## The walk-through

### Starting a workspace

**In the browser.** The desktop's welcome page has a form, `rel="create-form"`, labelled Start a temporary workspace. Beside it the page says where the work will be stored, how long it lasts and how much it may hold, before anything is uploaded. Pressing it POSTs the form, and the service responds with `303` and the new workspace's address, `/w/k7f3/`. The workspace is private to the browser that started it, through a cookie, and it expires when its stated time is up.

**In the terminal.** A terminal is always opened inside a workspace, so it starts at the workspace's address, which the desktop gives it when the window opens. Its prompt shows the workspace and the current folder.

### Creating the file

**In the browser.** Files shows the folder `notes`. The folder's representation lists its entries as links with `rel="item"` and includes a form with `rel="create-form"` and a `name` field. The reader chooses New document, types `report.md` and presses Create. The service creates the file with its first revision, which is empty, and responds with `303` and the file's identity address. Files shows the new entry, and the desktop opens it in the Editor.

**In the terminal.** `touch notes/report.md` does the same. The terminal's file layer fetches the folder `notes`, finds the form with `rel="create-form"`, fills in `name`, sends it and follows the redirect. If the name is already taken, the service returns `422` and the form with its error, and the terminal prints the error's text.

### Writing it

**In the browser.** The Editor shows the Markdown source, with the rendered preview beside it in a split. The first change the writer makes creates a draft: the file's representation includes a form with `rel="working-copy"`, and the Editor sends it with `base` set to the current revision. The draft is a resource of its own, with its own address, linked back to the file with `rel="working-copy-of"`. While the writer types, the Editor saves the draft every few seconds through the draft's own `edit-form`. A draft belongs to one person and one file, so closing the window, reloading the page or losing the connection loses at most a few seconds of typing. The window's state in the address includes the draft's address, so reloading brings the draft back.

Pressing Save sends the draft's form with `rel="https://pudl.parkscomputing.com/rel/commit"`. If the file's latest revision is still the draft's base, the service makes a new revision from the draft's text, discards the draft and responds with `303` and the file. Revisions are immutable, each at an address of its own, linked from the file with `rel="version-history"` and to each other with `rel="predecessor-version"`.

**In the terminal.** `edit notes/report.md` opens the terminal's own text editor on the latest revision. Saving it sends the file's `edit-form` directly, with `base` set to the revision it opened, and makes a new revision the same way. The terminal does not keep a draft, because its editor runs inside one command and is either saved or abandoned when the command ends.

### Reading it elsewhere

**In the browser.** The reader opens the file in Preview from Files, through Open With. The file's representation links its rendered form with `rel="alternate" type="text/html"`, and Preview shows that page, headings, tables, task lists and all, with links to other documents in the workspace working as links. Preview checks every few seconds whether the file has changed, with a conditional request that costs almost nothing when it has not, and reloads when it has. Stage 1 does this by polling; a stream of change notices can replace it later if polling proves too slow or too costly.

**In the terminal.** `cat notes/report.md` prints the Markdown source, which the file's representation links with `rel="alternate" type="text/markdown"`. `open notes/report.md` asks the desktop to open the file in its default application, which for Markdown is the Editor, and `open -a preview notes/report.md` chooses Preview.

### A conflict

The writer has the report open in the Editor, with unsaved changes in a draft based on revision 3. In the terminal they run `echo "- Check the totals" >> notes/report.md`, which saves revision 4.

**In the browser.** When the writer presses Save, the draft's base, revision 3, is no longer the latest, so the service returns `409 Conflict` and a page that shows what happened. It compares, line by line and word by word, the base revision, the latest revision and the draft, and offers three forms. Keep my version makes a new revision from the draft. Take the latest discards the draft. Merge opens a merged text, with the lines that differ marked, in the Editor, where the writer finishes it and saves again against revision 4. The draft stays until one of the three is chosen, so nothing is lost, and Back from the conflict page leaves everything as it was.

**In the terminal.** The same conflict can happen the other way round. If the terminal's editor saves against a base that is no longer the latest, the terminal prints that the file has changed since it was opened, saves the text as a draft through the file's `working-copy` form so that it is not lost, and prints the address of the conflict page, which `open` shows in the browser. The terminal does not try to merge by itself.

### Exporting it

**In the browser.** The file's representation includes a form with `rel="https://pudl.parkscomputing.com/rel/export"` and a choice of format: an HTML page, a standalone HTML file with its styles inside it, or a printable page. The reader chooses one and presses Export. The service creates a job and responds with `303` and the job's address. The job's page shows its progress and, while it runs, a form to cancel it; when it finishes, the cancel form is gone and the page links the exported file with `rel="https://pudl.parkscomputing.com/rel/result"`, together with a download link. The job is a resource of its own, so closing the window does not stop it, and the desktop's job list shows it until the reader dismisses it.

**In the terminal.** `export notes/report.md html` follows the same form, prints the job's address and then its progress until it finishes, and prints the result's path. Ctrl+C stops the terminal waiting and leaves the job running; `jobs` lists jobs, and `jobs cancel` sends a job's cancel form if it has one.

### Deleting and restoring it

**In the browser.** The file's representation includes a form with `rel="https://pudl.parkscomputing.com/rel/trash"`, labelled Move to Trash. Pressing it moves the file into the workspace's Trash and responds with `303` and the Trash entry, which shows how long it will be kept and a Restore form. Files no longer lists the file in `notes`. A window that still shows the file says that it is in the Trash and offers the same Restore form. Documents that link to it now have a broken link, which the desktop reports in their own representations.

**In the terminal.** `rm notes/report.md` sends the same form and prints where the file went and until when. `trash` lists the Trash, and `restore notes/report.md` sends an entry's Restore form. Deleting a file for good is a separate form, Delete permanently, with a confirmation that says what will be lost; in a temporary workspace it is rarely needed, since everything goes when the workspace expires.

### Reloading and coming back

Reloading the desktop brings back every window as the address describes it, each showing its file by identity, with the Editor's draft restored. A temporary workspace can be exported as a bundle of its Markdown files before it expires. Sharing a private workspace's address with another person shows them nothing, since the workspace belongs to the browser that started it. Read-only examples can be shared freely.

## What the terminal needs

The Parks Computing terminal divides cleanly at its commands, which reach the outside world only through the input and output object a command is given. Below that line, though, its file layer assumes the whole tree is in memory and returns every result synchronously: path resolution, directory walks, completion and the search for runnable scripts all read the tree directly, and the working directory is a node in it rather than a path. That works for the site's small tree and cannot work for a service of revisions, drafts and jobs.

The desktop's terminal therefore keeps the terminal's line editor, history, completion engine, parser, pipes, scripts, pager, text editor and generic commands, and replaces the file layer with an asynchronous one.

- The new layer works by path and never exposes nodes. Its operations are `stat`, `list`, `read`, `write`, `mkdir`, `remove`, `move`, `copy` and `upload`, each taking an `AbortSignal`, so that Ctrl+C cancels a request already in flight.
- The layer is the only part of the terminal that understands hypermedia. It fetches HTML, parses it with the browser's `DOMParser`, and finds the links and forms it needs by their `rel` values. The commands never see HTML.
- The layer keeps a small cache of folder listings, revalidated with conditional requests, so that completion stays quick. Completion becomes asynchronous and ignores a result that arrives after the line has changed.
- The working directory becomes a path.
- `upload` opens the browser's file picker before anything else is awaited, because the picker must open while the keypress still counts as the reader's action.
- The site's own commands, such as `tags`, `guide` and the game launchers, move to an extras file, as the text commands already have. The desktop adds `export`, `jobs`, `trash`, `restore`, `log` for a file's revisions and `diff` between two revisions.

None of `terminal.js`, `sitefs.js`, `terminal-text.js` or `terminal.css` has a licence header, and the only licence file in the Parks Computing repository is MIT text with a Microsoft copyright that looks like template boilerplate. Paul wrote every commit to the terminal, so he can licence it as he chooses; the files should say so before they are copied into this repository. The vendored xterm.js is MIT, and its licence file goes with it.

## On a phone

The desktop follows YAVCHN, which already works well on a phone.

- At 640 pixels wide or less, the workspace shows one pane at a time: Places and Files, or the windows. Windows on a narrow screen are maximised, except windows sized by their content, which PUDL already lets float.
- The taskbar runs along the foot of the screen across both panes, with a button that switches between Places and the windows and arrows that scroll its tabs when they overflow.
- The desktop's height follows the browser's visible viewport, so the keyboard and the browser's own bars never hide the taskbar, and the taskbar keeps clear of the screen's safe areas.
- The Editor's source and preview become one pane at a time on a narrow screen, chosen by a Source, Preview and Split selector, as YAVCHN's Article, Discussion and Split selector works.

Several of these are general enough to belong in PUDL rather than in each site: a pane switch that is separate from minimising every window, a taskbar placed across both panes, scrolling arrows for an overflowing dock, the visible-viewport height, a documented compact title bar, and a way for a site to say which address parameters are its own window state. YAVCHN's PUDL proposal already asks for the first two. The desktop should consume them from a PUDL release rather than carry its own copies.

## Decisions this walk-through needs

1. **The Markdown dialect.** I propose CommonMark with GitHub's tables, task lists, strikethrough and autolinks, and YAML front matter. Footnotes, wiki-style `[[links]]` and any syntax for including one document in another are left out of stage 1.
2. **Two addresses for every file**, a path address for reading and linking and an identity address for windows, with old path addresses redirecting after a rename.
3. **What a rename does to links in other documents.** I propose that the desktop offers to rewrite them, as a job that lists the documents it will change, rather than changing them silently or leaving them broken.
4. **Drafts in the Editor and none in the terminal.** The Editor keeps a draft per person and file; the terminal's editor saves directly and makes a draft only when its save meets a conflict.
5. **One renderer.** goldmark on the server renders every Markdown view in stage 1, including the editor's live preview, which asks the server for the rendered HTML as the writer types.
6. **The temporary workspace's limits.** I propose that a workspace lasts 24 hours from its last use, holds at most 5 MB of text, and belongs to the browser that started it.
