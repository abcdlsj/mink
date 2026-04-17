package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"

	"github.com/abcdlsj/mink/msg"
	"github.com/abcdlsj/mink/session"
)

type DB struct {
	sql *sql.DB
}

func Open(path string) (*DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	s := &DB{sql: db}
	if err := s.init(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (d *DB) Close() error {
	if d == nil || d.sql == nil {
		return nil
	}
	return d.sql.Close()
}

func (d *DB) SaveSession(s *session.Session) error {
	tx, err := d.sql.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`
		INSERT INTO sessions (id, source, title, summary, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			source = excluded.source,
			title = excluded.title,
			summary = excluded.summary,
			updated_at = excluded.updated_at
	`, s.ID, s.Source, s.Title, s.Summary, s.CreatedAt.UTC().Format(time.RFC3339Nano), s.UpdatedAt.UTC().Format(time.RFC3339Nano)); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM messages WHERE session_id = ?`, s.ID); err != nil {
		return err
	}
	for i, m := range s.Messages {
		toolCalls, _ := json.Marshal(m.ToolCalls)
		toolResults, _ := json.Marshal(m.ToolResults)
		if _, err := tx.Exec(`
			INSERT INTO messages (
				session_id, seq, id, role, content, reasoning, reasoning_signature, tool_calls_json, tool_results_json, created_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, s.ID, i, m.ID, m.Role, m.Content, m.Reasoning, m.ReasoningSignature, string(toolCalls), string(toolResults), m.Timestamp.UTC().Format(time.RFC3339Nano)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (d *DB) LoadSession(id string) (*session.Session, error) {
	row := d.sql.QueryRow(`SELECT id, source, title, summary, created_at, updated_at FROM sessions WHERE id = ?`, id)
	var s session.Session
	var createdAt string
	var updatedAt string
	if err := row.Scan(&s.ID, &s.Source, &s.Title, &s.Summary, &createdAt, &updatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("session not found: %s", id)
		}
		return nil, err
	}
	s.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	s.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)

	rows, err := d.sql.Query(`
		SELECT id, role, content, reasoning, reasoning_signature, tool_calls_json, tool_results_json, created_at
		FROM messages WHERE session_id = ? ORDER BY seq ASC
	`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var m msg.Message
		var toolCalls string
		var toolResults string
		var created string
		if err := rows.Scan(&m.ID, &m.Role, &m.Content, &m.Reasoning, &m.ReasoningSignature, &toolCalls, &toolResults, &created); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(toolCalls), &m.ToolCalls)
		_ = json.Unmarshal([]byte(toolResults), &m.ToolResults)
		m.Timestamp, _ = time.Parse(time.RFC3339Nano, created)
		s.Messages = append(s.Messages, m)
	}
	return &s, rows.Err()
}

func (d *DB) ListSessions() ([]*session.Session, error) {
	rows, err := d.sql.Query(`
		SELECT id, source, title, summary, created_at, updated_at
		FROM sessions ORDER BY updated_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*session.Session
	for rows.Next() {
		var s session.Session
		var createdAt string
		var updatedAt string
		if err := rows.Scan(&s.ID, &s.Source, &s.Title, &s.Summary, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		s.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
		s.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
		out = append(out, &s)
	}
	return out, rows.Err()
}

func (d *DB) CurrentSessionID(source string) (string, error) {
	row := d.sql.QueryRow(`SELECT value FROM settings WHERE key = ?`, "source:"+source)
	var id string
	if err := row.Scan(&id); err != nil {
		if err == sql.ErrNoRows {
			return "", nil
		}
		return "", err
	}
	return id, nil
}

func (d *DB) SetCurrentSession(source, id string) error {
	_, err := d.sql.Exec(`
		INSERT INTO settings (key, value)
		VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value
	`, "source:"+source, id)
	return err
}

func (d *DB) init() error {
	schema := []string{
		`CREATE TABLE IF NOT EXISTS sessions (
			id TEXT PRIMARY KEY,
			source TEXT NOT NULL,
			title TEXT NOT NULL DEFAULT '',
			summary TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS messages (
			session_id TEXT NOT NULL,
			seq INTEGER NOT NULL,
			id TEXT NOT NULL,
			role TEXT NOT NULL,
			content TEXT NOT NULL DEFAULT '',
			reasoning TEXT NOT NULL DEFAULT '',
			reasoning_signature TEXT NOT NULL DEFAULT '',
			tool_calls_json TEXT NOT NULL DEFAULT '[]',
			tool_results_json TEXT NOT NULL DEFAULT '[]',
			created_at TEXT NOT NULL,
			PRIMARY KEY (session_id, seq)
		)`,
		`CREATE TABLE IF NOT EXISTS settings (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		)`,
	}
	for _, stmt := range schema {
		if _, err := d.sql.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}
