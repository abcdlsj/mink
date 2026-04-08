package sqlite

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	zsqlite "zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

type OpenOptions struct {
	PoolSize int
}

type DB struct {
	path string
	pool *sqlitex.Pool
}

func Open(path string, opts OpenOptions) (*DB, error) {
	if path == "" {
		return nil, fmt.Errorf("sqlite: empty path")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}

	pool, err := sqlitex.NewPool(path, sqlitex.PoolOptions{
		PoolSize: opts.PoolSize,
	})
	if err != nil {
		return nil, err
	}

	db := &DB{path: path, pool: pool}
	if err := db.WithConn(context.Background(), func(conn *zsqlite.Conn) error {
		if err := sqlitex.ExecuteScript(conn, schema, nil); err != nil {
			return err
		}
		return migrate(conn)
	}); err != nil {
		_ = pool.Close()
		return nil, err
	}
	return db, nil
}

func (db *DB) Path() string {
	return db.path
}

func (db *DB) Close() error {
	if db == nil || db.pool == nil {
		return nil
	}
	return db.pool.Close()
}

func (db *DB) Conn(ctx context.Context) (*zsqlite.Conn, error) {
	if db == nil || db.pool == nil {
		return nil, fmt.Errorf("sqlite: nil db")
	}
	conn, err := db.pool.Take(ctx)
	if err != nil {
		return nil, err
	}
	if err := prepare(conn); err != nil {
		db.pool.Put(conn)
		return nil, err
	}
	return conn, nil
}

func (db *DB) Put(conn *zsqlite.Conn) {
	if db == nil || db.pool == nil || conn == nil {
		return
	}
	db.pool.Put(conn)
}

func (db *DB) WithConn(ctx context.Context, fn func(*zsqlite.Conn) error) error {
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer db.Put(conn)
	return fn(conn)
}

func (db *DB) Tx(ctx context.Context, fn func(*zsqlite.Conn) error) error {
	return db.WithConn(ctx, func(conn *zsqlite.Conn) error {
		if err := begin(conn); err != nil {
			return err
		}
		done := false
		defer func() {
			if !done {
				_ = rollback(conn)
			}
		}()
		if err := fn(conn); err != nil {
			return err
		}
		if err := commit(conn); err != nil {
			return err
		}
		done = true
		return nil
	})
}

func prepare(conn *zsqlite.Conn) error {
	if err := sqlitex.ExecuteTransient(conn, "PRAGMA foreign_keys=ON", nil); err != nil {
		return err
	}
	if err := sqlitex.ExecuteTransient(conn, "PRAGMA busy_timeout=5000", nil); err != nil {
		return err
	}
	return nil
}

func migrate(conn *zsqlite.Conn) error {
	for _, stmt := range []string{
		`ALTER TABLE source_bindings ADD COLUMN team_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE source_bindings ADD COLUMN team_thread_id TEXT NOT NULL DEFAULT ''`,
	} {
		if err := sqlitex.ExecuteTransient(conn, stmt, nil); err != nil && !isDuplicateColumnErr(err) {
			return err
		}
	}
	return nil
}

func isDuplicateColumnErr(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "duplicate column name: team_id") ||
		strings.Contains(msg, "duplicate column name: team_thread_id")
}
