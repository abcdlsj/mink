package session

import (
	"context"

	zsqlite "zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

func (s *SQLiteStore) Delete(id string) error {
	if s == nil || s.db == nil {
		return s.errNil()
	}
	return s.db.WithConn(context.Background(), func(conn *zsqlite.Conn) error {
		return sqlitex.ExecuteTransient(conn, `
			DELETE FROM sessions
			WHERE workspace_id = ? AND id = ?
		`, &sqlitex.ExecOptions{
			Args: []any{s.workspaceID, id},
		})
	})
}

func (s *SQLiteStore) List() ([]string, error) {
	if s == nil || s.db == nil {
		return nil, s.errNil()
	}
	var ids []string
	err := s.db.WithConn(context.Background(), func(conn *zsqlite.Conn) error {
		return sqlitex.ExecuteTransient(conn, `
			SELECT id
			FROM sessions
			WHERE workspace_id = ?
			ORDER BY updated_at DESC, id DESC
		`, &sqlitex.ExecOptions{
			Args: []any{s.workspaceID},
			ResultFunc: func(stmt *zsqlite.Stmt) error {
				ids = append(ids, stmt.ColumnText(0))
				return nil
			},
		})
	})
	return ids, err
}
