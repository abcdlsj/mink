package store

import (
	"bytes"
	"database/sql"
	"errors"
	"io"
	"path/filepath"
	"testing"
)

func TestOpenFailsClosedWhenCursorKeyIsUnavailable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.db")
	for _, random := range []io.Reader{nil, cursorKeyErrorReader{}} {
		closes := 0
		db, err := openWithOpts(path, random, func(db *sql.DB) error {
			closes++
			return db.Close()
		})
		if db != nil || !errors.Is(err, ErrCursorKeyUnavailable) || closes != 1 {
			t.Fatalf("open with unavailable random = %v, %v, closes %d", db, err, closes)
		}
	}
}

func TestOpenFailsClosedWhenPersistedCursorKeyIsCorrupt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if err := configureDB(raw); err != nil {
		raw.Close()
		t.Fatal(err)
	}
	if _, err := raw.Exec(`PRAGMA ignore_check_constraints = ON`); err != nil {
		raw.Close()
		t.Fatal(err)
	}
	if _, err := raw.Exec(`UPDATE work_cursor_keys SET key = X'01' WHERE singleton = 1`); err != nil {
		raw.Close()
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	failed, err := openWithRand(path, bytes.NewReader(bytes.Repeat([]byte{1}, 32)))
	if failed != nil || !errors.Is(err, ErrCursorKeyUnavailable) {
		t.Fatalf("open with corrupt key = %v, %v", failed, err)
	}
}

type cursorKeyErrorReader struct{}

func (cursorKeyErrorReader) Read([]byte) (int, error) {
	return 0, errors.New("random unavailable")
}
