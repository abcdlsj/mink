package sqlite

import (
	"context"
	"fmt"

	"github.com/abcdlsj/mink/runtime/id"
	zsqlite "zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

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

		taskID, err := db.activeTaskID(conn, key)
		if err != nil {
			return err
		}
		if taskID == "" {
			taskID = id.Task()
			if err := db.insertTask(conn, taskID, taskKind, trimTitle(title), key, now); err != nil {
				return err
			}
			if err := db.appendTaskCreated(conn, taskID, taskKind, trimTitle(title), agentID, source); err != nil {
				return err
			}
		} else {
			status, err := db.taskStatus(conn, taskID)
			if err != nil {
				return err
			}
			if !status.Resumable() && status != TaskRunning {
				taskID = id.Task()
				if err := db.insertTask(conn, taskID, taskKind, trimTitle(title), key, now); err != nil {
					return err
				}
				if err := db.appendTaskCreated(conn, taskID, taskKind, trimTitle(title), agentID, source); err != nil {
					return err
				}
			}
		}

		if err := sqlitex.ExecuteTransient(conn, `
			INSERT INTO source_bindings (
				workspace_id, source_kind, source_id, thread_id, active_task_id, active_session_id, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(workspace_id, source_kind, source_id, thread_id)
			DO UPDATE SET
				active_task_id = excluded.active_task_id,
				active_session_id = excluded.active_session_id,
				updated_at = excluded.updated_at
		`, &sqlitex.ExecOptions{
			Args: []any{db.WorkspaceID(), key.Kind, key.ID, key.ThreadID, taskID, sessionID, now},
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
			WHERE id = ? AND workspace_id = ?
		`, &sqlitex.ExecOptions{
			Args: []any{state.RunID, now, taskID, db.WorkspaceID()},
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
			WHERE id = ? AND workspace_id = ?
		`, &sqlitex.ExecOptions{
			Args: []any{taskSt, now, state.TaskID, db.WorkspaceID()},
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

func (db *DB) ResumeTask(ctx context.Context, taskID, agentID, sessionID, source string) (RunState, error) {
	if db == nil || taskID == "" {
		return RunState{}, nil
	}

	now := nowString()
	state := RunState{TaskID: taskID, RunID: id.Run()}

	return state, db.Tx(ctx, func(conn *zsqlite.Conn) error {
		status, err := db.taskStatus(conn, taskID)
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
			WHERE id = ? AND workspace_id = ?
		`, &sqlitex.ExecOptions{
			Args: []any{state.RunID, now, taskID, db.WorkspaceID()},
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
