package sqlite

import (
	"context"
	"strings"

	zsqlite "zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

type TeamSourceBinding struct {
	Source   string
	TeamID   string
	ThreadID string
}

func (db *DB) SessionBindings(ctx context.Context) (map[string]string, error) {
	if db == nil {
		return nil, nil
	}
	out := make(map[string]string)
	err := db.WithConn(ctx, func(conn *zsqlite.Conn) error {
		return sqlitex.ExecuteTransient(conn, `
			SELECT source_id, active_session_id
			FROM source_bindings
			WHERE workspace_id = ? AND active_session_id <> ''
		`, &sqlitex.ExecOptions{
			Args: []any{db.WorkspaceID()},
			ResultFunc: func(stmt *zsqlite.Stmt) error {
				out[stmt.ColumnText(0)] = stmt.ColumnText(1)
				return nil
			},
		})
	})
	return out, err
}

func (db *DB) TeamSourceBindings(ctx context.Context) ([]TeamSourceBinding, error) {
	if db == nil {
		return nil, nil
	}
	var out []TeamSourceBinding
	err := db.WithConn(ctx, func(conn *zsqlite.Conn) error {
		return sqlitex.ExecuteTransient(conn, `
			SELECT source_id, team_id, team_thread_id
			FROM source_bindings
			WHERE workspace_id = ? AND team_id <> '' AND team_thread_id <> ''
		`, &sqlitex.ExecOptions{
			Args: []any{db.WorkspaceID()},
			ResultFunc: func(stmt *zsqlite.Stmt) error {
				out = append(out, TeamSourceBinding{
					Source:   stmt.ColumnText(0),
					TeamID:   stmt.ColumnText(1),
					ThreadID: stmt.ColumnText(2),
				})
				return nil
			},
		})
	})
	return out, err
}

func (db *DB) UpsertTeamSourceBinding(ctx context.Context, source, teamID, teamThreadID string) error {
	if db == nil || strings.TrimSpace(source) == "" {
		return nil
	}
	now := nowString()
	key := parseSource(source)
	return db.WithConn(ctx, func(conn *zsqlite.Conn) error {
		return sqlitex.ExecuteTransient(conn, `
			INSERT INTO source_bindings (
				workspace_id, source_kind, source_id, thread_id, active_task_id, active_session_id, team_id, team_thread_id, updated_at
			) VALUES (?, ?, ?, ?, '', '', ?, ?, ?)
			ON CONFLICT(workspace_id, source_kind, source_id, thread_id)
			DO UPDATE SET
				team_id = excluded.team_id,
				team_thread_id = excluded.team_thread_id,
				updated_at = excluded.updated_at
		`, &sqlitex.ExecOptions{
			Args: []any{db.WorkspaceID(), key.Kind, key.ID, key.ThreadID, teamID, teamThreadID, now},
		})
	})
}

func (db *DB) ClearTeamSourceBinding(ctx context.Context, source string) error {
	if db == nil || strings.TrimSpace(source) == "" {
		return nil
	}
	key := parseSource(source)
	return db.WithConn(ctx, func(conn *zsqlite.Conn) error {
		return sqlitex.ExecuteTransient(conn, `
			UPDATE source_bindings
			SET team_id = '', team_thread_id = '', updated_at = ?
			WHERE workspace_id = ? AND source_kind = ? AND source_id = ? AND thread_id = ?
		`, &sqlitex.ExecOptions{
			Args: []any{nowString(), db.WorkspaceID(), key.Kind, key.ID, key.ThreadID},
		})
	})
}
