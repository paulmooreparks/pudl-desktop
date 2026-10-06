// Copyright 2026 Paul Parks. SPDX-License-Identifier: Apache-2.0

// Package store keeps PUDL Desktop's workspaces, folders, files and
// revisions. Metadata lives in SQLite. The bytes of each revision live in
// a file of their own under the data folder, named by their SHA-256 hash
// within their workspace, and are written and made durable before the
// metadata that publishes them is committed, so an interrupted write can
// never replace a good revision. A file written whose metadata was never
// committed is unreferenced, and the sweep removes it.
package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// Kinds of entry.
const (
	Folder = "folder"
	File   = "file"
)

// The limits of a temporary workspace (docs/walkthrough.md, decision 6).
// Entries are limited as well as bytes, since folders and empty files take
// no quota, and the number of live workspaces is limited so that the whole
// store has a ceiling: Workspaces times Quota, a little under 1 GB.
const (
	Lifetime   = 24 * time.Hour
	Quota      = 5 << 20
	Entries    = 1000
	Workspaces = 190
)

var (
	ErrNotFound = errors.New("not found")
	ErrExists   = errors.New("an entry with that name already exists here")
	ErrName     = errors.New("a name may not be empty, start with a dot, or contain a slash")
	ErrQuota    = errors.New("the workspace has no room for this; it may hold 5 MB of text")
	ErrNotText  = errors.New("only UTF-8 text without NUL characters can be stored")
	ErrEntries  = errors.New("the workspace has no room for this; it may hold 1,000 files and folders")
	ErrFull     = errors.New("the desktop has as many temporary workspaces as it can hold")
	ErrNotDir   = errors.New("not a folder")
	ErrNotFile  = errors.New("not a file")
)

// Conflict is returned when a change was made against a revision that is
// no longer the latest.
type Conflict struct {
	Base, Latest int
}

func (c *Conflict) Error() string {
	return fmt.Sprintf("the file is at revision %d, not %d", c.Latest, c.Base)
}

type Store struct {
	db   *sql.DB
	data string
	now  func() time.Time
}

type Workspace struct {
	ID       string
	Root     string
	Created  time.Time
	LastUsed time.Time
}

// Expires is when the workspace goes, unless it is used before then.
func (w *Workspace) Expires() time.Time { return w.LastUsed.Add(Lifetime) }

type Entry struct {
	ID, Workspace, Parent, Name, Kind string
	Created                           time.Time
	Latest                            int // the latest revision of a file
}

type Revision struct {
	File    string
	Number  int
	Size    int64
	Hash    string
	Created time.Time
}

const schema = `
PRAGMA foreign_keys = ON;
CREATE TABLE IF NOT EXISTS workspaces (
  id TEXT PRIMARY KEY,
  token_hash TEXT NOT NULL,
  root TEXT NOT NULL,
  created INTEGER NOT NULL,
  last_used INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS entries (
  id TEXT PRIMARY KEY,
  workspace TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  parent TEXT,
  name TEXT NOT NULL,
  kind TEXT NOT NULL,
  created INTEGER NOT NULL,
  latest INTEGER NOT NULL DEFAULT 0
);
CREATE UNIQUE INDEX IF NOT EXISTS entries_name ON entries(workspace, parent, name);
CREATE TABLE IF NOT EXISTS revisions (
  file TEXT NOT NULL REFERENCES entries(id) ON DELETE CASCADE,
  number INTEGER NOT NULL,
  size INTEGER NOT NULL,
  hash TEXT NOT NULL,
  created INTEGER NOT NULL,
  PRIMARY KEY (file, number)
);
CREATE TABLE IF NOT EXISTS submissions (
  token TEXT PRIMARY KEY,
  workspace TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  location TEXT NOT NULL,
  created INTEGER NOT NULL
);
`

// Open opens or creates the store in the data folder.
func Open(data string) (*Store, error) {
	if err := os.MkdirAll(filepath.Join(data, "objects"), 0o700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", "file:"+filepath.Join(data, "desktop.db")+"?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db, data: data, now: time.Now}, nil
}

func (s *Store) Close() error { return s.db.Close() }

// Secret returns a secret of the service's own, made the first time it is
// asked for and kept in the data folder, so that what it signs stays
// valid across restarts.
func (s *Store) Secret(name string) ([]byte, error) {
	path := filepath.Join(s.data, name+".key")
	if b, err := os.ReadFile(path); err == nil && len(b) == 32 {
		return b, nil
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return nil, err
	}
	return b, nil
}

// NewID returns a random identifier of n bytes, written in lowercase
// base-32 without padding.
func NewID(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	const alphabet = "0123456789abcdefghjkmnpqrstvwxyz"
	var sb strings.Builder
	var acc, bits uint
	for _, c := range b {
		acc = acc<<8 | uint(c)
		bits += 8
		for bits >= 5 {
			bits -= 5
			sb.WriteByte(alphabet[(acc>>bits)&31])
		}
	}
	if bits > 0 {
		sb.WriteByte(alphabet[(acc<<(5-bits))&31])
	}
	return sb.String()
}

func hashToken(t string) string {
	h := sha256.Sum256([]byte(t))
	return hex.EncodeToString(h[:])
}

func unix(t time.Time) int64     { return t.UnixMilli() }
func fromUnix(n int64) time.Time { return time.UnixMilli(n) }

// CreateWorkspace starts a temporary workspace with an empty root folder
// and returns it with the secret token that proves ownership of it.
func (s *Store) CreateWorkspace(ctx context.Context) (*Workspace, string, error) {
	now := s.now()
	ws := &Workspace{ID: NewID(8), Root: NewID(8), Created: now, LastUsed: now}
	token := NewID(32)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, "", err
	}
	defer tx.Rollback()
	// Expired workspaces not yet swept do not count against the limit.
	var live int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM workspaces WHERE last_used >= ?`, unix(now.Add(-Lifetime))).Scan(&live); err != nil {
		return nil, "", err
	}
	if live >= Workspaces {
		return nil, "", ErrFull
	}
	if _, err := tx.Exec(`INSERT INTO workspaces (id, token_hash, root, created, last_used) VALUES (?, ?, ?, ?, ?)`,
		ws.ID, hashToken(token), ws.Root, unix(now), unix(now)); err != nil {
		return nil, "", err
	}
	if _, err := tx.Exec(`INSERT INTO entries (id, workspace, parent, name, kind, created) VALUES (?, ?, NULL, '', ?, ?)`,
		ws.Root, ws.ID, Folder, unix(now)); err != nil {
		return nil, "", err
	}
	return ws, token, tx.Commit()
}

// Workspace returns a workspace whose token matches, and marks it used.
// A wrong token and a missing or expired workspace are both ErrNotFound,
// so a stranger cannot tell which workspaces exist.
func (s *Store) Workspace(ctx context.Context, id, token string) (*Workspace, error) {
	var ws Workspace
	var th string
	var created, used int64
	err := s.db.QueryRowContext(ctx, `SELECT id, token_hash, root, created, last_used FROM workspaces WHERE id = ?`, id).
		Scan(&ws.ID, &th, &ws.Root, &created, &used)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, err
	}
	if subtle.ConstantTimeCompare([]byte(th), []byte(hashToken(token))) != 1 {
		return nil, ErrNotFound
	}
	ws.Created, ws.LastUsed = fromUnix(created), fromUnix(used)
	now := s.now()
	if now.After(ws.Expires()) {
		return nil, ErrNotFound
	}
	// Marking a workspace used costs a write, so it is done at most once a
	// minute.
	if now.Sub(ws.LastUsed) > time.Minute {
		if _, err := s.db.ExecContext(ctx, `UPDATE workspaces SET last_used = ? WHERE id = ?`, unix(now), id); err != nil {
			return nil, err
		}
		ws.LastUsed = now
	}
	return &ws, nil
}

func scanEntry(row interface{ Scan(...any) error }) (*Entry, error) {
	var e Entry
	var parent sql.NullString
	var created int64
	if err := row.Scan(&e.ID, &e.Workspace, &parent, &e.Name, &e.Kind, &created, &e.Latest); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	e.Parent, e.Created = parent.String, fromUnix(created)
	return &e, nil
}

const entryCols = `id, workspace, parent, name, kind, created, latest`

// Entry returns an entry of the workspace by its identity.
func (s *Store) Entry(ctx context.Context, ws, id string) (*Entry, error) {
	return scanEntry(s.db.QueryRowContext(ctx, `SELECT `+entryCols+` FROM entries WHERE workspace = ? AND id = ?`, ws, id))
}

// Resolve finds the entry at a path of names below the workspace's root.
func (s *Store) Resolve(ctx context.Context, w *Workspace, names []string) (*Entry, error) {
	e, err := s.Entry(ctx, w.ID, w.Root)
	for _, n := range names {
		if err != nil {
			return nil, err
		}
		if n == "" {
			continue
		}
		if e.Kind != Folder {
			return nil, ErrNotFound
		}
		e, err = scanEntry(s.db.QueryRowContext(ctx,
			`SELECT `+entryCols+` FROM entries WHERE workspace = ? AND parent = ? AND name = ?`, w.ID, e.ID, n))
	}
	return e, err
}

// Path returns the names from the root down to the entry, the root's own
// empty name left out.
func (s *Store) Path(ctx context.Context, e *Entry) ([]string, error) {
	var names []string
	for e.Parent != "" {
		names = append([]string{e.Name}, names...)
		p, err := s.Entry(ctx, e.Workspace, e.Parent)
		if err != nil {
			return nil, err
		}
		e = p
	}
	return names, nil
}

// Children lists a folder's entries, folders first, then by name.
func (s *Store) Children(ctx context.Context, folder *Entry) ([]*Entry, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+entryCols+` FROM entries WHERE workspace = ? AND parent = ?
		ORDER BY kind = 'file', name COLLATE NOCASE`, folder.Workspace, folder.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Entry
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func validName(n string) bool {
	return n != "" && n != "." && n != ".." && !strings.HasPrefix(n, ".") &&
		!strings.ContainsAny(n, "/\\\x00") && len(n) <= 255
}

// Once runs a change at most once per submission token. If the token has
// been used, it returns the location the first run returned and replayed
// true, and does not run the change again.
func (s *Store) Once(ctx context.Context, ws, token string, change func(tx *sql.Tx) (string, error)) (location string, replayed bool, err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", false, err
	}
	defer tx.Rollback()
	if token != "" {
		var loc string
		err := tx.QueryRow(`SELECT location FROM submissions WHERE token = ? AND workspace = ?`, token, ws).Scan(&loc)
		if err == nil {
			return loc, true, nil
		} else if !errors.Is(err, sql.ErrNoRows) {
			return "", false, err
		}
	}
	loc, err := change(tx)
	if err != nil {
		return "", false, err
	}
	if token != "" {
		if _, err := tx.Exec(`INSERT INTO submissions (token, workspace, location, created) VALUES (?, ?, ?, ?)`,
			token, ws, loc, unix(s.now())); err != nil {
			return "", false, err
		}
	}
	return loc, false, tx.Commit()
}

// Create makes a folder or an empty file in a folder, within a change
// begun by Once. A file is created with an empty first revision.
func (s *Store) Create(tx *sql.Tx, folder *Entry, name, kind string) (*Entry, error) {
	if folder.Kind != Folder {
		return nil, ErrNotDir
	}
	if !validName(name) {
		return nil, ErrName
	}
	if kind != Folder && kind != File {
		return nil, fmt.Errorf("unknown kind %q", kind)
	}
	var n int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM entries WHERE workspace = ? AND parent = ? AND name = ?`,
		folder.Workspace, folder.ID, name).Scan(&n); err != nil {
		return nil, err
	}
	if n > 0 {
		return nil, ErrExists
	}
	if err := tx.QueryRow(`SELECT COUNT(*) FROM entries WHERE workspace = ?`, folder.Workspace).Scan(&n); err != nil {
		return nil, err
	}
	if n > Entries { // the root folder is not counted
		return nil, ErrEntries
	}
	now := s.now()
	e := &Entry{ID: NewID(8), Workspace: folder.Workspace, Parent: folder.ID, Name: name, Kind: kind, Created: now}
	if _, err := tx.Exec(`INSERT INTO entries (id, workspace, parent, name, kind, created) VALUES (?, ?, ?, ?, ?, ?)`,
		e.ID, e.Workspace, e.Parent, e.Name, e.Kind, unix(now)); err != nil {
		return nil, err
	}
	if kind == File {
		if _, err := s.writeRevision(tx, e, 0, nil); err != nil {
			return nil, err
		}
		e.Latest = 1
	}
	return e, nil
}

// Save makes a new revision of a file from text, if base is still its
// latest revision; otherwise it returns a *Conflict and changes nothing.
func (s *Store) Save(tx *sql.Tx, file *Entry, base int, text []byte) (int, error) {
	if file.Kind != File {
		return 0, ErrNotFile
	}
	return s.writeRevision(tx, file, base, text)
}

func (s *Store) writeRevision(tx *sql.Tx, file *Entry, base int, text []byte) (int, error) {
	if !isText(text) {
		return 0, ErrNotText
	}
	var latest int
	if err := tx.QueryRow(`SELECT latest FROM entries WHERE id = ?`, file.ID).Scan(&latest); err != nil {
		return 0, err
	}
	if latest != base {
		return 0, &Conflict{Base: base, Latest: latest}
	}
	var used int64
	if err := tx.QueryRow(`SELECT COALESCE(SUM(r.size), 0) FROM revisions r JOIN entries e ON e.id = r.file WHERE e.workspace = ?`,
		file.Workspace).Scan(&used); err != nil {
		return 0, err
	}
	if used+int64(len(text)) > Quota {
		return 0, ErrQuota
	}
	hash, err := s.putObject(file.Workspace, text)
	if err != nil {
		return 0, err
	}
	next := latest + 1
	if _, err := tx.Exec(`INSERT INTO revisions (file, number, size, hash, created) VALUES (?, ?, ?, ?, ?)`,
		file.ID, next, len(text), hash, unix(s.now())); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(`UPDATE entries SET latest = ? WHERE id = ?`, next, file.ID); err != nil {
		return 0, err
	}
	file.Latest = next
	return next, nil
}

func isText(b []byte) bool {
	return !strings.ContainsRune(string(b), 0) && strings.ToValidUTF8(string(b), "�") == string(b)
}

func (s *Store) objectPath(ws, hash string) string {
	return filepath.Join(s.data, "objects", ws, hash[:2], hash)
}

// putObject writes bytes under their hash, durably, before the caller
// commits the metadata that refers to them. Bytes already stored are not
// written again.
func (s *Store) putObject(ws string, b []byte) (string, error) {
	sum := sha256.Sum256(b)
	hash := hex.EncodeToString(sum[:])
	path := s.objectPath(ws, hash)
	if _, err := os.Stat(path); err == nil {
		return hash, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "put-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return "", err
	}
	return hash, nil
}

// Revision returns one revision of a file.
func (s *Store) Revision(ctx context.Context, file *Entry, n int) (*Revision, error) {
	r := Revision{File: file.ID}
	var created int64
	err := s.db.QueryRowContext(ctx, `SELECT number, size, hash, created FROM revisions WHERE file = ? AND number = ?`, file.ID, n).
		Scan(&r.Number, &r.Size, &r.Hash, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, err
	}
	r.Created = fromUnix(created)
	return &r, nil
}

// Revisions lists a file's revisions, newest first.
func (s *Store) Revisions(ctx context.Context, file *Entry) ([]*Revision, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT number, size, hash, created FROM revisions WHERE file = ? ORDER BY number DESC`, file.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Revision
	for rows.Next() {
		r := Revision{File: file.ID}
		var created int64
		if err := rows.Scan(&r.Number, &r.Size, &r.Hash, &created); err != nil {
			return nil, err
		}
		r.Created = fromUnix(created)
		out = append(out, &r)
	}
	return out, rows.Err()
}

// Read returns a revision's text.
func (s *Store) Read(file *Entry, r *Revision) ([]byte, error) {
	return os.ReadFile(s.objectPath(file.Workspace, r.Hash))
}

// Sweep deletes expired workspaces and their bytes, and bytes no revision
// refers to that are older than an hour, which an interrupted save leaves.
func (s *Store) Sweep(ctx context.Context) error {
	cutoff := unix(s.now().Add(-Lifetime))
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM workspaces WHERE last_used < ?`, cutoff)
	if err != nil {
		return err
	}
	var gone []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		gone = append(gone, id)
	}
	rows.Close()
	for _, id := range gone {
		if _, err := s.db.ExecContext(ctx, `DELETE FROM workspaces WHERE id = ?`, id); err != nil {
			return err
		}
		if err := os.RemoveAll(filepath.Join(s.data, "objects", id)); err != nil {
			return err
		}
	}
	return s.sweepObjects(ctx)
}

func (s *Store) sweepObjects(ctx context.Context) error {
	root := filepath.Join(s.data, "objects")
	old := s.now().Add(-time.Hour)
	return filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		info, err := d.Info()
		if err != nil || info.ModTime().After(old) {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		parts := strings.Split(filepath.ToSlash(rel), "/")
		if len(parts) != 3 || strings.HasPrefix(parts[2], "put-") {
			return os.Remove(path)
		}
		var n int
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM revisions r JOIN entries e ON e.id = r.file
			WHERE e.workspace = ? AND r.hash = ?`, parts[0], parts[2]).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			return os.Remove(path)
		}
		return nil
	})
}
