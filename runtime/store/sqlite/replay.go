package sqlite

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/abcdlsj/mink/msg"
	zsqlite "zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

type storedEvent struct {
	Type      string
	ActorType string
	ActorID   string
	Payload   string
	CreatedAt string
}

type ReplayEvent struct {
	Timestamp time.Time      `json:"timestamp"`
	Type      string         `json:"type"`
	Level     string         `json:"level"`
	StepNum   *int           `json:"step_num,omitempty"`
	Data      map[string]any `json:"data,omitempty"`
}

func (db *DB) MessagesForSource(ctx context.Context, source string, limit int) ([]msg.Message, error) {
	if db == nil || source == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 200
	}

	key := parseSource(source)
	var events []storedEvent
	err := db.WithConn(ctx, func(conn *zsqlite.Conn) error {
		taskID, err := db.taskIDForSource(conn, key)
		if err != nil {
			return err
		}
		if taskID == "" {
			return nil
		}
		return sqlitex.ExecuteTransient(conn, `
			SELECT type, actor_type, COALESCE(actor_id, ''), payload_json, created_at
			FROM events
			WHERE task_id = ?
			ORDER BY created_at ASC, seq ASC
		`, &sqlitex.ExecOptions{
			Args: []any{taskID},
			ResultFunc: func(stmt *zsqlite.Stmt) error {
				events = append(events, storedEvent{
					Type:      stmt.ColumnText(0),
					ActorType: stmt.ColumnText(1),
					ActorID:   stmt.ColumnText(2),
					Payload:   stmt.ColumnText(3),
					CreatedAt: stmt.ColumnText(4),
				})
				return nil
			},
		})
	})
	if err != nil {
		return nil, err
	}

	msgs := make([]msg.Message, 0, len(events))
	for _, ev := range events {
		if m, ok := eventToMessage(ev); ok {
			msgs = append(msgs, m)
		}
	}
	if len(msgs) > limit {
		msgs = msgs[len(msgs)-limit:]
	}
	return msgs, nil
}

func (db *DB) ReplayEventsForSession(ctx context.Context, sessionID string, limit int) ([]ReplayEvent, error) {
	if db == nil || sessionID == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 30
	}

	var rows []storedEvent
	err := db.WithConn(ctx, func(conn *zsqlite.Conn) error {
		return sqlitex.ExecuteTransient(conn, `
			SELECT e.type, e.actor_type, COALESCE(e.actor_id, ''), e.payload_json, e.created_at
			FROM events e
			JOIN runs r ON r.id = e.run_id
			WHERE r.session_id = ?
			ORDER BY e.created_at DESC, e.seq DESC
			LIMIT ?
		`, &sqlitex.ExecOptions{
			Args: []any{sessionID, limit},
			ResultFunc: func(stmt *zsqlite.Stmt) error {
				rows = append(rows, storedEvent{
					Type:      stmt.ColumnText(0),
					ActorType: stmt.ColumnText(1),
					ActorID:   stmt.ColumnText(2),
					Payload:   stmt.ColumnText(3),
					CreatedAt: stmt.ColumnText(4),
				})
				return nil
			},
		})
	})
	if err != nil {
		return nil, err
	}

	out := make([]ReplayEvent, 0, len(rows))
	for i := len(rows) - 1; i >= 0; i-- {
		ev, ok := replayEventFromStored(rows[i])
		if ok {
			out = append(out, ev)
		}
	}
	return out, nil
}

func (db *DB) MessagesForTask(ctx context.Context, taskID string, limit int) ([]msg.Message, error) {
	if db == nil || taskID == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 200
	}

	var events []storedEvent
	err := db.WithConn(ctx, func(conn *zsqlite.Conn) error {
		return sqlitex.ExecuteTransient(conn, `
			SELECT type, actor_type, COALESCE(actor_id, ''), payload_json, created_at
			FROM events
			WHERE task_id = ?
			ORDER BY created_at ASC, seq ASC
		`, &sqlitex.ExecOptions{
			Args: []any{taskID},
			ResultFunc: func(stmt *zsqlite.Stmt) error {
				events = append(events, storedEvent{
					Type:      stmt.ColumnText(0),
					ActorType: stmt.ColumnText(1),
					ActorID:   stmt.ColumnText(2),
					Payload:   stmt.ColumnText(3),
					CreatedAt: stmt.ColumnText(4),
				})
				return nil
			},
		})
	})
	if err != nil {
		return nil, err
	}

	msgs := make([]msg.Message, 0, len(events))
	for _, ev := range events {
		if m, ok := eventToMessage(ev); ok {
			msgs = append(msgs, m)
		}
	}
	if len(msgs) > limit {
		msgs = msgs[len(msgs)-limit:]
	}
	return msgs, nil
}

func (db *DB) MessagesForRun(ctx context.Context, runID string, limit int) ([]msg.Message, error) {
	if db == nil || runID == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 200
	}

	var events []storedEvent
	err := db.WithConn(ctx, func(conn *zsqlite.Conn) error {
		return sqlitex.ExecuteTransient(conn, `
			SELECT type, actor_type, COALESCE(actor_id, ''), payload_json, created_at
			FROM events
			WHERE run_id = ?
			ORDER BY seq ASC
		`, &sqlitex.ExecOptions{
			Args: []any{runID},
			ResultFunc: func(stmt *zsqlite.Stmt) error {
				events = append(events, storedEvent{
					Type:      stmt.ColumnText(0),
					ActorType: stmt.ColumnText(1),
					ActorID:   stmt.ColumnText(2),
					Payload:   stmt.ColumnText(3),
					CreatedAt: stmt.ColumnText(4),
				})
				return nil
			},
		})
	})
	if err != nil {
		return nil, err
	}

	msgs := make([]msg.Message, 0, len(events))
	for _, ev := range events {
		if m, ok := eventToMessage(ev); ok {
			msgs = append(msgs, m)
		}
	}
	if len(msgs) > limit {
		msgs = msgs[len(msgs)-limit:]
	}
	return msgs, nil
}

func eventToMessage(ev storedEvent) (msg.Message, bool) {
	type payload struct {
		ID      string          `json:"id"`
		Content string          `json:"content"`
		Output  string          `json:"output"`
		Error   string          `json:"error"`
		Name    string          `json:"name"`
		Args    json.RawMessage `json:"args"`
		AgentID string          `json:"agent_id"`
	}
	var p payload
	if ev.Payload != "" {
		_ = json.Unmarshal([]byte(ev.Payload), &p)
	}
	timestamp, _ := time.Parse(time.RFC3339Nano, ev.CreatedAt)

	agentID := p.AgentID
	if agentID == "" {
		agentID = ev.ActorID
	}

	switch ev.Type {
	case "session.compacted":
		if p.Content == "" {
			p.Content = p.Output
		}
		if p.Content == "" {
			var raw map[string]string
			if json.Unmarshal([]byte(ev.Payload), &raw) == nil {
				p.Content = raw["summary"]
			}
		}
		if p.Content == "" {
			return msg.Message{}, false
		}
		return msg.Message{Role: "system", Content: "[Context Anchor]\n" + p.Content, Timestamp: timestamp, AgentID: agentID}, true
	case "input.received":
		role := ev.ActorType
		if role == "" {
			role = "user"
		}
		if p.Content == "" {
			return msg.Message{}, false
		}
		return msg.Message{Role: role, Content: p.Content, Timestamp: timestamp, AgentID: agentID}, true
	case "assistant.emitted":
		if p.Content == "" {
			return msg.Message{}, false
		}
		return msg.Message{Role: "assistant", Content: p.Content, Timestamp: timestamp, AgentID: agentID}, true
	case "tool.called":
		if p.ID == "" || p.Name == "" {
			return msg.Message{}, false
		}
		return msg.Message{
			Role: "assistant",
			ToolCalls: []msg.ToolCall{{
				ID:   p.ID,
				Name: p.Name,
				Args: p.Args,
			}},
			Timestamp: timestamp,
			AgentID:   agentID,
		}, true
	case "tool.completed", "tool.failed":
		content := p.Output
		if ev.Type == "tool.failed" && p.Error != "" && content == "" {
			content = p.Error
		}
		if content == "" && p.Error == "" {
			return msg.Message{}, false
		}
		return msg.Message{
			Role: "tool",
			ToolResults: []msg.ToolResult{{
				ToolCallID: p.ID,
				Content:    content,
				Error:      p.Error,
			}},
			Timestamp: timestamp,
			AgentID:   agentID,
		}, true
	default:
		return msg.Message{}, false
	}
}

func replayEventFromStored(ev storedEvent) (ReplayEvent, bool) {
	var data map[string]any
	if ev.Payload != "" {
		_ = json.Unmarshal([]byte(ev.Payload), &data)
	}
	timestamp, _ := time.Parse(time.RFC3339Nano, ev.CreatedAt)
	out := ReplayEvent{
		Timestamp: timestamp,
		Type:      ev.Type,
		Level:     "info",
		Data:      data,
	}
	switch ev.Type {
	case "input.received":
		out.Type = "user_input"
		if out.Data == nil {
			out.Data = make(map[string]any)
		}
		if v, ok := out.Data["content"].(string); ok && strings.TrimSpace(v) != "" {
			out.Data["input"] = v
		}
	case "assistant.emitted":
		out.Type = "agent_output"
	case "tool.called":
		out.Type = "tool_call"
	case "tool.completed", "tool.failed":
		out.Type = "tool_end"
		if ev.Type == "tool.failed" {
			out.Level = "error"
		}
	case "llm.empty_response", "llm.empty_fallback":
		out.Level = "warn"
	case "session.compacted":
		if out.Data == nil {
			out.Data = make(map[string]any)
		}
		if v, ok := out.Data["content"].(string); ok && strings.TrimSpace(v) != "" {
			out.Data["summary"] = v
		}
	}
	return out, true
}

func (db *DB) taskIDForSource(conn *zsqlite.Conn, key sourceKey) (string, error) {
	taskID, err := db.activeTaskID(conn, key)
	if err != nil || taskID != "" {
		return taskID, err
	}
	err = sqlitex.ExecuteTransient(conn, `
		SELECT id
		FROM tasks
		WHERE workspace_id = ? AND source_kind = ? AND source_id = ? AND thread_id = ?
		ORDER BY updated_at DESC
		LIMIT 1
	`, &sqlitex.ExecOptions{
		Args: []any{db.WorkspaceID(), key.Kind, key.ID, key.ThreadID},
		ResultFunc: func(stmt *zsqlite.Stmt) error {
			taskID = stmt.ColumnText(0)
			return nil
		},
	})
	return taskID, err
}
