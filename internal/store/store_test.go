package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestServerIDPersistsAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.db")
	first := openServerID(t, path)
	second := openServerID(t, path)
	if first != second {
		t.Fatalf("server id changed from %q to %q", first, second)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("database mode = %o, want 600", got)
	}
}

func openServerID(t *testing.T, path string) string {
	t.Helper()
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	id, err := store.ServerID(context.Background())
	if err != nil {
		store.Close()
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	return id
}
