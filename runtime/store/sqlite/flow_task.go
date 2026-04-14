package sqlite

import (
	"context"
	"fmt"
	"strings"

	"github.com/abcdlsj/mink/runtime/id"
	zsqlite "zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

func (db *DB) ResetSource(ctx context.Context, source string) error {
	if db == nil {
		return nil
	}

	key := parseSource(source)
	return db.WithConn(ctx, func(conn *zsqlite.Conn) error {
		return sqlitex.ExecuteTransient(conn, `
			DELETE FROM source_bindings
			WHERE workspace_id = ? AND source_kind = ? AND source_id = ? AND thread_id = ?
		`, &sqlitex.ExecOptions{
			Args: []any{db.WorkspaceID(), key.Kind, key.ID, key.ThreadID},
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
			WHERE id = ? AND workspace_id = ?
		`, &sqlitex.ExecOptions{
			Args: []any{now, now, taskID, db.WorkspaceID()},
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
			FROM tasks WHERE workspace_id = ? AND id = ?
		`, &sqlitex.ExecOptions{
			Args: []any{db.WorkspaceID(), taskID},
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

	where := []string{"workspace_id = ?"}
	args := []any{db.WorkspaceID()}
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
				id, workspace_id, kind, title, status, source_kind, source_id, thread_id,
				parent_task_id, current_run_id, metadata_json, created_at, updated_at
			) VALUES (?, ?, ?, ?, 'queued', ?, ?, ?, ?, '', '{}', ?, ?)
		`, &sqlitex.ExecOptions{
			Args: []any{taskID, db.WorkspaceID(), kind, trimTitle(title), key.Kind, key.ID, key.ThreadID, parentTaskID, now, now},
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

func (db *DB) insertTask(conn *zsqlite.Conn, taskID, kind, title string, key sourceKey, now string) error {
	return sqlitex.ExecuteTransient(conn, `
		INSERT INTO tasks (
			id, workspace_id, kind, title, status, source_kind, source_id, thread_id,
			current_run_id, metadata_json, created_at, updated_at
		) VALUES (?, ?, ?, ?, 'queued', ?, ?, ?, '', '{}', ?, ?)
	`, &sqlitex.ExecOptions{
		Args: []any{taskID, db.WorkspaceID(), kind, title, key.Kind, key.ID, key.ThreadID, now, now},
	})
}

func (db *DB) appendTaskCreated(conn *zsqlite.Conn, taskID, kind, title, actorID, source string) error {
	return appendEventConn(conn, Event{
		TaskID:    taskID,
		Type:      "task.created",
		ActorType: "system",
		ActorID:   actorID,
		Source:    source,
		Payload: map[string]any{
			"kind":  kind,
			"title": title,
		},
	})
}

func (db *DB) taskStatus(conn *zsqlite.Conn, taskID string) (TaskStatus, error) {
	var status TaskStatus
	err := sqlitex.ExecuteTransient(conn, `
		SELECT status FROM tasks WHERE workspace_id = ? AND id = ?
	`, &sqlitex.ExecOptions{
		Args: []any{db.WorkspaceID(), taskID},
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
