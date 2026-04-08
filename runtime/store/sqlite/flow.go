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

type TaskStatus string

const (
	TaskQueued  TaskStatus = "queued"
	TaskRunning TaskStatus = "running"
	TaskWaiting TaskStatus = "waiting"
	TaskFailed  TaskStatus = "failed"
	TaskDone    TaskStatus = "done"
)

func (s TaskStatus) Resumable() bool {
	return s == TaskQueued || s == TaskWaiting
}

type RunStatus string

const (
	RunRunning     RunStatus = "running"
	RunCompleted   RunStatus = "completed"
	RunFailed      RunStatus = "failed"
	RunInterrupted RunStatus = "interrupted"
)

type RunState struct {
	TaskID string
	RunID  string
}

type TaskInfo struct {
	ID           string
	Kind         string
	Title        string
	Status       TaskStatus
	Priority     int
	SourceKind   string
	SourceID     string
	ThreadID     string
	ParentTaskID string
	CurrentRunID string
	CreatedAt    string
	UpdatedAt    string
	ClosedAt     string
}

type TaskListOptions struct {
	Status       TaskStatus
	SourceKind   string
	SourceID     string
	ParentTaskID string
	Limit        int
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
		} else {
			status, err := taskStatus(conn, taskID)
			if err != nil {
				return err
			}
			if !status.Resumable() && status != TaskRunning {
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
	runSt := string(RunCompleted)
	taskSt := string(TaskWaiting)
	errMsg := ""
	if runErr != nil {
		runSt = string(RunFailed)
		taskSt = string(TaskFailed)
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
			Args: []any{runSt, now, errMsg, state.RunID},
		}); err != nil {
			return err
		}

		if err := sqlitex.ExecuteTransient(conn, `
			UPDATE tasks
			SET status = ?, updated_at = ?
			WHERE id = ?
		`, &sqlitex.ExecOptions{
			Args: []any{taskSt, now, state.TaskID},
		}); err != nil {
			return err
		}
		if err := appendEventConn(conn, Event{
			TaskID:    state.TaskID,
			RunID:     state.RunID,
			Type:      "run." + runSt,
			ActorType: "agent",
			ActorID:   "",
			Payload: map[string]any{
				"task_status": taskSt,
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

func (db *DB) ResumeTask(ctx context.Context, taskID, agentID, sessionID, source string) (RunState, error) {
	if db == nil || taskID == "" {
		return RunState{}, nil
	}

	now := nowString()
	state := RunState{TaskID: taskID, RunID: id.Run()}

	return state, db.Tx(ctx, func(conn *zsqlite.Conn) error {
		status, err := taskStatus(conn, taskID)
		if err != nil {
			return err
		}
		if !status.Resumable() {
			return fmt.Errorf("task %s is %s, not resumable", taskID, status)
		}

		if err := sqlitex.ExecuteTransient(conn, `
			INSERT INTO runs (
				id, task_id, agent_id, trigger, status, started_at, session_id, metadata_json
			) VALUES (?, ?, ?, 'resume', 'running', ?, ?, '{}')
		`, &sqlitex.ExecOptions{
			Args: []any{state.RunID, taskID, agentID, now, sessionID},
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

		return appendEventConn(conn, Event{
			TaskID:    taskID,
			RunID:     state.RunID,
			Type:      "task.resumed",
			ActorType: "agent",
			ActorID:   agentID,
			Source:    source,
			Payload: map[string]any{
				"from_status": string(status),
				"session_id":  sessionID,
			},
		})
	})
}

func (db *DB) CompleteTask(ctx context.Context, taskID string) error {
	if db == nil || taskID == "" {
		return nil
	}
	now := nowString()
	return db.WithConn(ctx, func(conn *zsqlite.Conn) error {
		return sqlitex.ExecuteTransient(conn, `
			UPDATE tasks
			SET status = 'done', closed_at = ?, updated_at = ?
			WHERE id = ?
		`, &sqlitex.ExecOptions{
			Args: []any{now, now, taskID},
		})
	})
}

func (db *DB) GetTask(ctx context.Context, taskID string) (TaskInfo, error) {
	var t TaskInfo
	if db == nil || taskID == "" {
		return t, nil
	}
	err := db.WithConn(ctx, func(conn *zsqlite.Conn) error {
		return sqlitex.ExecuteTransient(conn, `
			SELECT id, kind, title, status, priority, source_kind, source_id, thread_id,
				COALESCE(parent_task_id, ''), current_run_id, created_at, updated_at, COALESCE(closed_at, '')
			FROM tasks WHERE id = ?
		`, &sqlitex.ExecOptions{
			Args: []any{taskID},
			ResultFunc: func(stmt *zsqlite.Stmt) error {
				t = TaskInfo{
					ID:           stmt.ColumnText(0),
					Kind:         stmt.ColumnText(1),
					Title:        stmt.ColumnText(2),
					Status:       TaskStatus(stmt.ColumnText(3)),
					Priority:     stmt.ColumnInt(4),
					SourceKind:   stmt.ColumnText(5),
					SourceID:     stmt.ColumnText(6),
					ThreadID:     stmt.ColumnText(7),
					ParentTaskID: stmt.ColumnText(8),
					CurrentRunID: stmt.ColumnText(9),
					CreatedAt:    stmt.ColumnText(10),
					UpdatedAt:    stmt.ColumnText(11),
					ClosedAt:     stmt.ColumnText(12),
				}
				return nil
			},
		})
	})
	return t, err
}

func (db *DB) ListTasks(ctx context.Context, opts TaskListOptions) ([]TaskInfo, error) {
	if db == nil {
		return nil, nil
	}
	if opts.Limit <= 0 {
		opts.Limit = 50
	}

	var where []string
	var args []any
	if opts.Status != "" {
		where = append(where, "status = ?")
		args = append(args, string(opts.Status))
	}
	if opts.SourceKind != "" {
		where = append(where, "source_kind = ?")
		args = append(args, opts.SourceKind)
	}
	if opts.SourceID != "" {
		where = append(where, "source_id = ?")
		args = append(args, opts.SourceID)
	}
	if opts.ParentTaskID != "" {
		where = append(where, "parent_task_id = ?")
		args = append(args, opts.ParentTaskID)
	}

	q := "SELECT id, kind, title, status, priority, source_kind, source_id, thread_id, COALESCE(parent_task_id, ''), current_run_id, created_at, updated_at, COALESCE(closed_at, '') FROM tasks"
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += " ORDER BY updated_at DESC LIMIT ?"
	args = append(args, opts.Limit)

	var tasks []TaskInfo
	err := db.WithConn(ctx, func(conn *zsqlite.Conn) error {
		return sqlitex.ExecuteTransient(conn, q, &sqlitex.ExecOptions{
			Args: args,
			ResultFunc: func(stmt *zsqlite.Stmt) error {
				tasks = append(tasks, TaskInfo{
					ID:           stmt.ColumnText(0),
					Kind:         stmt.ColumnText(1),
					Title:        stmt.ColumnText(2),
					Status:       TaskStatus(stmt.ColumnText(3)),
					Priority:     stmt.ColumnInt(4),
					SourceKind:   stmt.ColumnText(5),
					SourceID:     stmt.ColumnText(6),
					ThreadID:     stmt.ColumnText(7),
					ParentTaskID: stmt.ColumnText(8),
					CurrentRunID: stmt.ColumnText(9),
					CreatedAt:    stmt.ColumnText(10),
					UpdatedAt:    stmt.ColumnText(11),
					ClosedAt:     stmt.ColumnText(12),
				})
				return nil
			},
		})
	})
	return tasks, err
}

func (db *DB) CreateChildTask(ctx context.Context, parentTaskID, kind, title, agentID, source string) (string, error) {
	if db == nil {
		return "", nil
	}
	now := nowString()
	key := parseSource(source)
	taskID := id.Task()

	return taskID, db.Tx(ctx, func(conn *zsqlite.Conn) error {
		if err := sqlitex.ExecuteTransient(conn, `
			INSERT INTO tasks (
				id, kind, title, status, source_kind, source_id, thread_id,
				parent_task_id, current_run_id, metadata_json, created_at, updated_at
			) VALUES (?, ?, ?, 'queued', ?, ?, ?, ?, '', '{}', ?, ?)
		`, &sqlitex.ExecOptions{
			Args: []any{taskID, kind, trimTitle(title), key.Kind, key.ID, key.ThreadID, parentTaskID, now, now},
		}); err != nil {
			return err
		}
		return appendEventConn(conn, Event{
			TaskID:    taskID,
			Type:      "task.created",
			ActorType: "agent",
			ActorID:   agentID,
			Source:    source,
			Payload: map[string]any{
				"kind":           kind,
				"title":          trimTitle(title),
				"parent_task_id": parentTaskID,
			},
		})
	})
}

func taskStatus(conn *zsqlite.Conn, taskID string) (TaskStatus, error) {
	var status TaskStatus
	err := sqlitex.ExecuteTransient(conn, `
		SELECT status FROM tasks WHERE id = ?
	`, &sqlitex.ExecOptions{
		Args: []any{taskID},
		ResultFunc: func(stmt *zsqlite.Stmt) error {
			status = TaskStatus(stmt.ColumnText(0))
			return nil
		},
	})
	if status == "" {
		return "", fmt.Errorf("task %s not found", taskID)
	}
	return status, err
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
