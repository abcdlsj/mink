package sqlite

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	zsqlite "zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

type OpenOptions struct {
	PoolSize  int
	Workspace string
}

type DB struct {
	path          string
	pool          *sqlitex.Pool
	workspaceID   string
	workspacePath string
	workspaceName string
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

	db := &DB{
		path: path,
		pool: pool,
	}
	ws := ResolveWorkspace(opts.Workspace)
	db.workspaceID = ws.ID
	db.workspacePath = ws.Path
	db.workspaceName = ws.Name
	reset, err := db.needsReset(context.Background())
	if err != nil {
		_ = pool.Close()
		return nil, err
	}
	if reset {
		_ = pool.Close()
		if err := resetDB(path); err != nil {
			return nil, err
		}
		pool, err = sqlitex.NewPool(path, sqlitex.PoolOptions{
			PoolSize: opts.PoolSize,
		})
		if err != nil {
			return nil, err
		}
		db.pool = pool
	}
	if err := db.WithConn(context.Background(), func(conn *zsqlite.Conn) error {
		if err := sqlitex.ExecuteScript(conn, schema, nil); err != nil {
			return err
		}
		return db.ensureWorkspace(conn)
	}); err != nil {
		_ = pool.Close()
		return nil, err
	}
	return db, nil
}

func (db *DB) Path() string {
	return db.path
}

func (db *DB) WorkspaceID() string {
	if db == nil {
		return defaultWorkspaceID
	}
	if db.workspaceID == "" {
		return defaultWorkspaceID
	}
	return db.workspaceID
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

func (db *DB) needsReset(ctx context.Context) (bool, error) {
	if db == nil {
		return false, nil
	}
	var version int
	var hasObjects bool
	err := db.WithConn(ctx, func(conn *zsqlite.Conn) error {
		if err := sqlitex.ExecuteTransient(conn, `PRAGMA user_version`, &sqlitex.ExecOptions{
			ResultFunc: func(stmt *zsqlite.Stmt) error {
				version = stmt.ColumnInt(0)
				return nil
			},
		}); err != nil {
			return err
		}
		return sqlitex.ExecuteTransient(conn, `
			SELECT 1
			FROM sqlite_master
			WHERE name NOT LIKE 'sqlite_%'
			LIMIT 1
		`, &sqlitex.ExecOptions{
			ResultFunc: func(stmt *zsqlite.Stmt) error {
				hasObjects = stmt.ColumnInt(0) == 1
				return nil
			},
		})
	})
	if err != nil {
		return false, err
	}
	if !hasObjects {
		return false, nil
	}
	return version != schemaVersion, nil
}

func resetDB(path string) error {
	for _, name := range []string{path, path + "-wal", path + "-shm"} {
		if err := os.Remove(name); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func (db *DB) ensureWorkspace(conn *zsqlite.Conn) error {
	if db == nil || conn == nil {
		return nil
	}
	now := nowString()
	return sqlitex.ExecuteTransient(conn, `
		INSERT INTO workspaces (id, path, name, kind, status, metadata_json, created_at, updated_at)
		VALUES (?, ?, ?, 'local', 'active', '{}', ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			path = excluded.path,
			name = excluded.name,
			updated_at = excluded.updated_at
	`, &sqlitex.ExecOptions{
		Args: []any{
			db.WorkspaceID(),
			db.workspacePath,
			db.workspaceName,
			now,
			now,
		},
		})
}
