package sqlite

import (
	"context"

	zsqlite "zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

type staleRun struct {
	TaskID string
	RunID  string
}

func (db *DB) Recover(ctx context.Context) error {
	if db == nil {
		return nil
	}

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

		var stale []staleRun
		if err := sqlitex.ExecuteTransient(conn, `
			SELECT runs.task_id, runs.id
			FROM runs
			JOIN tasks ON tasks.id = runs.task_id
			WHERE runs.status = 'running' AND tasks.workspace_id = ?
			ORDER BY runs.started_at ASC
		`, &sqlitex.ExecOptions{
			Args: []any{db.WorkspaceID()},
			ResultFunc: func(stmt *zsqlite.Stmt) error {
				stale = append(stale, staleRun{
					TaskID: stmt.ColumnText(0),
					RunID:  stmt.ColumnText(1),
				})
				return nil
			},
		}); err != nil {
			return err
		}
		if len(stale) == 0 {
			if err := commit(conn); err != nil {
				return err
			}
			done = true
			return nil
		}

		now := nowString()
		for _, run := range stale {
			if err := sqlitex.ExecuteTransient(conn, `
				UPDATE runs
				SET status = 'interrupted',
					finished_at = COALESCE(finished_at, ?),
					error_message = CASE
						WHEN error_message = '' THEN 'interrupted by process restart'
						ELSE error_message
					END
				WHERE id = ?
			`, &sqlitex.ExecOptions{
				Args: []any{now, run.RunID},
			}); err != nil {
				return err
			}
			if err := sqlitex.ExecuteTransient(conn, `
				UPDATE tasks
				SET status = 'waiting',
					updated_at = ?
				WHERE id = ? AND workspace_id = ? AND status = 'running'
			`, &sqlitex.ExecOptions{
				Args: []any{now, run.TaskID, db.WorkspaceID()},
			}); err != nil {
				return err
			}
			if err := appendEventConn(conn, Event{
				TaskID:    run.TaskID,
				RunID:     run.RunID,
				Type:      "run.interrupted",
				ActorType: "system",
				ActorID:   "recovery",
				Payload: map[string]any{
					"reason": "process_restart",
				},
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
