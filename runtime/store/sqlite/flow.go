package sqlite

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/abcdlsj/mink/runtime/id"
	zsqlite "zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

type RunState struct {
	TaskID string
	RunID  string
}

func (db *DB) StartRun(ctx context.Context, source, sessionID, agentID, trigger, title string) (RunState, error) {
	if db == nil {
		return RunState{}, nil
	}

	now := nowString()
	key := parseSource(source)
	state := RunState{RunID: id.Run()}
	taskKind := taskKind(trigger)

	err := db.WithConn(ctx, func(conn *zsqlite.Conn) error {
		if err := begin(conn); err != nil {
			return err
		}
		done := false
		defer func() {
			if !done {
				_ = rollback(conn)
			}
		}()

		taskID, err := activeTaskID(conn, key)
		if err != nil {
			return err
		}
		if taskID == "" {
			taskID = id.Task()
			if err := sqlitex.ExecuteTransient(conn, `
				INSERT INTO tasks (
					id, kind, title, status, source_kind, source_id, thread_id,
					current_run_id, metadata_json, created_at, updated_at
				) VALUES (?, ?, ?, 'queued', ?, ?, ?, '', '{}', ?, ?)
			`, &sqlitex.ExecOptions{
				Args: []any{taskID, taskKind, trimTitle(title), key.Kind, key.ID, key.ThreadID, now, now},
			}); err != nil {
				return err
			}
			if err := appendEventConn(conn, Event{
				TaskID:    taskID,
				Type:      "task.created",
				ActorType: "system",
				ActorID:   agentID,
				Source:    source,
				Payload: map[string]any{
					"kind":  taskKind,
					"title": trimTitle(title),
				},
			}); err != nil {
				return err
			}
		}

		if err := sqlitex.ExecuteTransient(conn, `
			INSERT INTO source_bindings (
				source_kind, source_id, thread_id, active_task_id, active_session_id, updated_at
			) VALUES (?, ?, ?, ?, ?, ?)
			ON CONFLICT(source_kind, source_id, thread_id)
			DO UPDATE SET
				active_task_id = excluded.active_task_id,
				active_session_id = excluded.active_session_id,
				updated_at = excluded.updated_at
		`, &sqlitex.ExecOptions{
			Args: []any{key.Kind, key.ID, key.ThreadID, taskID, sessionID, now},
		}); err != nil {
			return err
		}

		if err := sqlitex.ExecuteTransient(conn, `
			INSERT INTO runs (
				id, task_id, agent_id, trigger, status, started_at, session_id, metadata_json
			) VALUES (?, ?, ?, ?, 'running', ?, ?, '{}')
		`, &sqlitex.ExecOptions{
			Args: []any{state.RunID, taskID, agentID, trigger, now, sessionID},
		}); err != nil {
			return err
		}

		if err := sqlitex.ExecuteTransient(conn, `
			UPDATE tasks
			SET status = 'running', current_run_id = ?, updated_at = ?
			WHERE id = ?
		`, &sqlitex.ExecOptions{
			Args: []any{state.RunID, now, taskID},
		}); err != nil {
			return err
		}
		if err := appendEventConn(conn, Event{
			TaskID:    taskID,
			RunID:     state.RunID,
			Type:      "run.started",
			ActorType: "agent",
			ActorID:   agentID,
			Source:    source,
			Payload: map[string]any{
				"trigger":    trigger,
				"session_id": sessionID,
			},
		}); err != nil {
			return err
		}

		if err := commit(conn); err != nil {
			return err
		}
		done = true
		state.TaskID = taskID
		return nil
	})
	return state, err
}

func (db *DB) FinishRun(ctx context.Context, state RunState, runErr error) error {
	if db == nil || state.TaskID == "" || state.RunID == "" {
		return nil
	}

	now := nowString()
	runStatus := "completed"
	taskStatus := "waiting"
	errMsg := ""
	if runErr != nil {
		runStatus = "failed"
		taskStatus = "failed"
		errMsg = runErr.Error()
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

		if err := sqlitex.ExecuteTransient(conn, `
			UPDATE runs
			SET status = ?, finished_at = ?, error_message = ?
			WHERE id = ?
		`, &sqlitex.ExecOptions{
			Args: []any{runStatus, now, errMsg, state.RunID},
		}); err != nil {
			return err
		}

		if err := sqlitex.ExecuteTransient(conn, `
			UPDATE tasks
			SET status = ?, updated_at = ?
			WHERE id = ?
		`, &sqlitex.ExecOptions{
			Args: []any{taskStatus, now, state.TaskID},
		}); err != nil {
			return err
		}
		if err := appendEventConn(conn, Event{
			TaskID:    state.TaskID,
			RunID:     state.RunID,
			Type:      "run." + runStatus,
			ActorType: "agent",
			ActorID:   "",
			Payload: map[string]any{
				"task_status": taskStatus,
				"error":       errMsg,
			},
		}); err != nil {
			return err
		}

		if err := commit(conn); err != nil {
			return err
		}
		done = true
		return nil
	})
}

func (db *DB) ResetSource(ctx context.Context, source string) error {
	if db == nil {
		return nil
	}

	key := parseSource(source)
	return db.WithConn(ctx, func(conn *zsqlite.Conn) error {
		return sqlitex.ExecuteTransient(conn, `
			DELETE FROM source_bindings
			WHERE source_kind = ? AND source_id = ? AND thread_id = ?
		`, &sqlitex.ExecOptions{
			Args: []any{key.Kind, key.ID, key.ThreadID},
		})
	})
}

type sourceKey struct {
	Kind     string
	ID       string
	ThreadID string
}

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

func activeTaskID(conn *zsqlite.Conn, key sourceKey) (string, error) {
	var taskID string
	err := sqlitex.ExecuteTransient(conn, `
		SELECT active_task_id
		FROM source_bindings
		WHERE source_kind = ? AND source_id = ? AND thread_id = ?
	`, &sqlitex.ExecOptions{
		Args: []any{key.Kind, key.ID, key.ThreadID},
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
