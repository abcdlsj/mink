package sqlite

import (
	"context"

	zsqlite "zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

func (db *DB) SessionBindings(ctx context.Context) (map[string]string, error) {
	if db == nil {
		return nil, nil
	}
	out := make(map[string]string)
	err := db.WithConn(ctx, func(conn *zsqlite.Conn) error {
		return sqlitex.ExecuteTransient(conn, `
			SELECT source_id, active_session_id
			FROM source_bindings
			WHERE active_session_id <> ''
		`, &sqlitex.ExecOptions{
			ResultFunc: func(stmt *zsqlite.Stmt) error {
				out[stmt.ColumnText(0)] = stmt.ColumnText(1)
				return nil
			},
		})
	})
	return out, err
}
