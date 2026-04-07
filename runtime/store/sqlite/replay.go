package sqlite

import (
	"context"
	"encoding/json"
	"time"

	"github.com/abcdlsj/mink/msg"
	zsqlite "zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

type storedEvent struct {
	Type      string
	ActorType string
	Payload   string
	CreatedAt string
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
		taskID, err := taskIDForSource(conn, key)
		if err != nil {
			return err
		}
		if taskID == "" {
			return nil
		}
		return sqlitex.ExecuteTransient(conn, `
			SELECT type, actor_type, payload_json, created_at
			FROM events
			WHERE task_id = ?
			ORDER BY created_at ASC, seq ASC
		`, &sqlitex.ExecOptions{
			Args: []any{taskID},
			ResultFunc: func(stmt *zsqlite.Stmt) error {
				events = append(events, storedEvent{
					Type:      stmt.ColumnText(0),
					ActorType: stmt.ColumnText(1),
					Payload:   stmt.ColumnText(2),
					CreatedAt: stmt.ColumnText(3),
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
		ID      string `json:"id"`
		Content string `json:"content"`
		Output  string `json:"output"`
		Error   string `json:"error"`
	}
	var p payload
	if ev.Payload != "" {
		_ = json.Unmarshal([]byte(ev.Payload), &p)
	}
	timestamp, _ := time.Parse(time.RFC3339Nano, ev.CreatedAt)

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
		return msg.Message{Role: "system", Content: "[Context Anchor]\n" + p.Content, Timestamp: timestamp}, true
	case "input.received":
		role := ev.ActorType
		if role == "" {
			role = "user"
		}
		if p.Content == "" {
			return msg.Message{}, false
		}
		return msg.Message{Role: role, Content: p.Content, Timestamp: timestamp}, true
	case "assistant.emitted":
		if p.Content == "" {
			return msg.Message{}, false
		}
		return msg.Message{Role: "assistant", Content: p.Content, Timestamp: timestamp}, true
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
		}, true
	default:
		return msg.Message{}, false
	}
}

func taskIDForSource(conn *zsqlite.Conn, key sourceKey) (string, error) {
	taskID, err := activeTaskID(conn, key)
	if err != nil || taskID != "" {
		return taskID, err
	}
	err = sqlitex.ExecuteTransient(conn, `
		SELECT id
		FROM tasks
		WHERE source_kind = ? AND source_id = ? AND thread_id = ?
		ORDER BY updated_at DESC
		LIMIT 1
	`, &sqlitex.ExecOptions{
		Args: []any{key.Kind, key.ID, key.ThreadID},
		ResultFunc: func(stmt *zsqlite.Stmt) error {
			taskID = stmt.ColumnText(0)
			return nil
		},
	})
	return taskID, err
}
