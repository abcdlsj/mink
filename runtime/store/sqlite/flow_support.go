package sqlite

import (
	"fmt"
	"strings"
	"time"

	zsqlite "zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

func parseSource(source string) sourceKey {
	if source == "" {
		return sourceKey{Kind: "unknown"}
	}
	if strings.HasPrefix(source, "telegram:") {
		parts := strings.SplitN(source, ":", 3)
		if len(parts) == 3 {
			return sourceKey{
				Kind:     parts[0],
				ID:       parts[0] + ":" + parts[1],
				ThreadID: parts[2],
			}
		}
	}
	kind := source
	if i := strings.IndexByte(source, ':'); i >= 0 {
		kind = source[:i]
	}
	return sourceKey{
		Kind: kind,
		ID:   source,
	}
}

func taskKind(trigger string) string {
	switch trigger {
	case "cron":
		return "cron"
	default:
		return "conversation"
	}
}

func (db *DB) activeTaskID(conn *zsqlite.Conn, key sourceKey) (string, error) {
	var taskID string
	err := sqlitex.ExecuteTransient(conn, `
		SELECT active_task_id
		FROM source_bindings
		WHERE workspace_id = ? AND source_kind = ? AND source_id = ? AND thread_id = ?
	`, &sqlitex.ExecOptions{
		Args: []any{db.WorkspaceID(), key.Kind, key.ID, key.ThreadID},
		ResultFunc: func(stmt *zsqlite.Stmt) error {
			taskID = stmt.ColumnText(0)
			return nil
		},
	})
	return taskID, err
}

func trimTitle(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "conversation"
	}
	r := []rune(s)
	if len(r) > 120 {
		return string(r[:120])
	}
	return s
}

func nowString() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}

func begin(conn *zsqlite.Conn) error {
	return sqlitex.ExecuteTransient(conn, "BEGIN IMMEDIATE", nil)
}

func commit(conn *zsqlite.Conn) error {
	return sqlitex.ExecuteTransient(conn, "COMMIT", nil)
}

func rollback(conn *zsqlite.Conn) error {
	err := sqlitex.ExecuteTransient(conn, "ROLLBACK", nil)
	if err != nil && !strings.Contains(err.Error(), "no transaction is active") {
		return fmt.Errorf("sqlite rollback: %w", err)
	}
	return nil
}
