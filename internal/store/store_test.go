// Copyright 2026 Paul Parks. SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"
)

func open(t *testing.T) *Store {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestWorkspaceLimit(t *testing.T) {
	ctx := context.Background()
	st := open(t)
	start := time.Now()
	st.now = func() time.Time { return start }
	for i := 0; i < Workspaces; i++ {
		if _, _, err := st.CreateWorkspace(ctx); err != nil {
			t.Fatalf("workspace %d: %v", i+1, err)
		}
	}
	if _, _, err := st.CreateWorkspace(ctx); !errors.Is(err, ErrFull) {
		t.Fatalf("one workspace past the limit should be refused, got %v", err)
	}
	// Once they have expired they no longer count, even before the sweep.
	st.now = func() time.Time { return start.Add(Lifetime + time.Minute) }
	if _, _, err := st.CreateWorkspace(ctx); err != nil {
		t.Fatalf("expired workspaces should not count against the limit: %v", err)
	}
}

func TestEntryLimit(t *testing.T) {
	ctx := context.Background()
	st := open(t)
	ws, token, err := st.CreateWorkspace(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if ws, err = st.Workspace(ctx, ws.ID, token); err != nil {
		t.Fatal(err)
	}
	root, err := st.Entry(ctx, ws.ID, ws.Root)
	if err != nil {
		t.Fatal(err)
	}
	create := func(name string) error {
		_, _, err := st.Once(ctx, ws.ID, NewID(16), func(tx *sql.Tx) (string, error) {
			_, err := st.Create(tx, root, name, Folder)
			return "", err
		})
		return err
	}
	for i := 0; i < Entries; i++ {
		if err := create(fmt.Sprintf("f%d", i)); err != nil {
			t.Fatalf("entry %d: %v", i+1, err)
		}
	}
	if err := create("one-more"); !errors.Is(err, ErrEntries) {
		t.Fatalf("one entry past the limit should be refused, got %v", err)
	}
}
