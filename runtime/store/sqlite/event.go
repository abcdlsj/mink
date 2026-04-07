package sqlite

import (
	"context"
	"encoding/json"

	"github.com/abcdlsj/mink/runtime/id"
	zsqlite "zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

type Event struct {
	TaskID    string
	RunID     string
	Type      string
	ActorType string
	ActorID   string
	Source    string
	Payload   any
}

func (db *DB) AppendEvent(ctx context.Context, ev Event) error {
	if db == nil || ev.TaskID == "" || ev.Type == "" {
		return nil
	}
	return db.WithConn(ctx, func(conn *zsqlite.Conn) error {
		return appendEventConn(conn, ev)
	})
}

func appendEventConn(conn *zsqlite.Conn, ev Event) error {
	body, err := json.Marshal(ev.Payload)
	if err != nil {
		return err
	}
	key := parseSource(ev.Source)
	seq, err := nextSeq(conn, ev.RunID)
	if err != nil {
		return err
	}
	if err := sqlitex.ExecuteTransient(conn, `
		INSERT INTO events (
			id, task_id, run_id, seq, type, actor_type, actor_id,
			source_kind, source_id, thread_id, payload_json, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, &sqlitex.ExecOptions{
		Args: []any{
			id.Event(), ev.TaskID, nullable(ev.RunID), seq, ev.Type, nonEmpty(ev.ActorType, "system"),
			ev.ActorID, key.Kind, key.ID, key.ThreadID, string(body), nowString(),
		},
	}); err != nil {
		return err
	}
	if ev.RunID == "" {
		return nil
	}
	return sqlitex.ExecuteTransient(conn, `
		UPDATE runs
		SET last_event_seq = ?
		WHERE id = ?
	`, &sqlitex.ExecOptions{
		Args: []any{seq, ev.RunID},
	})
}

func nextSeq(conn *zsqlite.Conn, runID string) (int64, error) {
	var seq int64 = 1
	if runID == "" {
		return seq, nil
	}
	err := sqlitex.ExecuteTransient(conn, `
		SELECT COALESCE(MAX(seq), 0) + 1
		FROM events
		WHERE run_id = ?
	`, &sqlitex.ExecOptions{
		Args: []any{runID},
		ResultFunc: func(stmt *zsqlite.Stmt) error {
			seq = stmt.ColumnInt64(0)
			return nil
		},
	})
	return seq, err
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nonEmpty(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
