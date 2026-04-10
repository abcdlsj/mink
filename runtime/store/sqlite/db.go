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
		path:          path,
		pool:          pool,
		workspaceID:   workspaceID(opts.Workspace),
		workspacePath: strings.TrimSpace(opts.Workspace),
		workspaceName: workspaceName(opts.Workspace),
	}
	if err := db.WithConn(context.Background(), func(conn *zsqlite.Conn) error {
		if err := sqlitex.ExecuteScript(conn, schema, nil); err != nil && !isDeferredSchemaErr(err) {
			return err
		}
		if err := migrate(conn); err != nil {
			return err
		}
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

func migrate(conn *zsqlite.Conn) error {
	for _, stmt := range []string{
		`ALTER TABLE tasks ADD COLUMN workspace_id TEXT NOT NULL DEFAULT 'ws_default'`,
		`ALTER TABLE source_bindings ADD COLUMN workspace_id TEXT NOT NULL DEFAULT 'ws_default'`,
		`ALTER TABLE teams ADD COLUMN workspace_id TEXT NOT NULL DEFAULT 'ws_default'`,
		`ALTER TABLE team_threads ADD COLUMN workspace_id TEXT NOT NULL DEFAULT 'ws_default'`,
		`ALTER TABLE memory_docs ADD COLUMN scope_kind TEXT NOT NULL DEFAULT 'global'`,
		`ALTER TABLE memory_docs ADD COLUMN scope_key TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE source_bindings ADD COLUMN team_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE source_bindings ADD COLUMN team_thread_id TEXT NOT NULL DEFAULT ''`,
	} {
		if err := sqlitex.ExecuteTransient(conn, stmt, nil); err != nil && !isDuplicateColumnErr(err) {
			return err
		}
	}
	if err := rebuildAgentIdentities(conn); err != nil {
		return err
	}
	return nil
}

func rebuildAgentIdentities(conn *zsqlite.Conn) error {
	if conn == nil {
		return nil
	}
	legacy := true
	if err := sqlitex.ExecuteTransient(conn, `
		SELECT 1
		FROM pragma_table_info('agent_identities')
		WHERE name = 'workspace_id'
		LIMIT 1
	`, &sqlitex.ExecOptions{
		ResultFunc: func(stmt *zsqlite.Stmt) error {
			legacy = false
			return nil
		},
	}); err != nil {
		return err
	}
	if !legacy {
		return nil
	}
	if err := sqlitex.ExecuteScript(conn, `
		DROP TABLE IF EXISTS agent_identities_new;
		CREATE TABLE agent_identities_new (
		  workspace_id TEXT NOT NULL DEFAULT 'ws_default',
		  agent_id TEXT NOT NULL,
		  display_name TEXT NOT NULL DEFAULT '',
		  profile TEXT NOT NULL DEFAULT '',
		  memory_scope TEXT NOT NULL DEFAULT '',
		  tool_constraints_json TEXT NOT NULL DEFAULT '[]',
		  metadata_json TEXT NOT NULL DEFAULT '{}',
		  created_at TEXT NOT NULL,
		  updated_at TEXT NOT NULL,
		  PRIMARY KEY (workspace_id, agent_id)
		);
		INSERT INTO agent_identities_new (
		  workspace_id, agent_id, display_name, profile, memory_scope, tool_constraints_json, metadata_json, created_at, updated_at
		)
		SELECT
		  'ws_default', agent_id, display_name, profile, memory_scope,
		  COALESCE(tool_constraints_json, '[]'),
		  COALESCE(metadata_json, '{}'),
		  created_at, updated_at
		FROM agent_identities;
		DROP TABLE agent_identities;
		ALTER TABLE agent_identities_new RENAME TO agent_identities;
		CREATE INDEX IF NOT EXISTS idx_agent_identities_workspace_created
		ON agent_identities(workspace_id, created_at);
	`, nil); err != nil {
		return err
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

func isDuplicateColumnErr(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	if strings.Contains(msg, "duplicate column name: workspace_id") {
		return true
	}
	if strings.Contains(msg, "duplicate column name: scope_kind") {
		return true
	}
	if strings.Contains(msg, "duplicate column name: scope_key") {
		return true
	}
	return strings.Contains(msg, "duplicate column name: team_id") ||
		strings.Contains(msg, "duplicate column name: team_thread_id")
}

func isDeferredSchemaErr(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "no such column: workspace_id") ||
		strings.Contains(msg, "no such column: scope_kind") ||
		strings.Contains(msg, "no such column: agent_id")
}
