package sqlite

import (
	"context"

	zsqlite "zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

func (db *DB) CompactSource(ctx context.Context, source, summary, note string) error {
	if db == nil || source == "" || summary == "" {
		return nil
	}

	key := parseSource(source)
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

		taskID, err := taskIDForSource(conn, key)
		if err != nil || taskID == "" {
			return err
		}

		var runID string
		if err := sqlitex.ExecuteTransient(conn, `
			SELECT current_run_id
			FROM tasks
			WHERE id = ?
		`, &sqlitex.ExecOptions{
			Args: []any{taskID},
			ResultFunc: func(stmt *zsqlite.Stmt) error {
				runID = stmt.ColumnText(0)
				return nil
			},
		}); err != nil {
			return err
		}

		if err := sqlitex.ExecuteTransient(conn, `
			DELETE FROM events
			WHERE task_id = ?
			  AND type IN (
			    'input.received',
			    'assistant.emitted',
			    'tool.called',
			    'tool.completed',
			    'tool.failed',
			    'session.compacted'
			  )
		`, &sqlitex.ExecOptions{
			Args: []any{taskID},
		}); err != nil {
			return err
		}

		if err := appendEventConn(conn, Event{
			TaskID:    taskID,
			RunID:     runID,
			Type:      "session.compacted",
			ActorType: "system",
			ActorID:   "compaction",
			Source:    source,
			Payload: map[string]any{
				"summary": summary,
				"note":    note,
			},
		}); err != nil {
			return err
		}

		if runID != "" {
			if err := sqlitex.ExecuteTransient(conn, `
				UPDATE runs
				SET summary = ?
				WHERE id = ?
			`, &sqlitex.ExecOptions{
				Args: []any{summary, runID},
			}); err != nil {
				return err
			}
		}

		if err := commit(conn); err != nil {
			return err
		}
		done = true
		return nil
	})
}
