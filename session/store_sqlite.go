package session

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	rtsqlite "github.com/abcdlsj/mink/runtime/store/sqlite"
	zsqlite "zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

type SQLiteStore struct {
	db            *rtsqlite.DB
	workspaceID   string
	workspacePath string
	workspaceName string
}

func NewSQLiteStore(db *rtsqlite.DB, workspacePath string) *SQLiteStore {
	trimmed := strings.TrimSpace(workspacePath)
	return &SQLiteStore{
		db:            db,
		workspaceID:   workspaceID(trimmed),
		workspacePath: trimmed,
		workspaceName: workspaceName(trimmed),
	}
}

func (s *SQLiteStore) Load(id string) (*Snapshot, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("session: nil sqlite store")
	}
	var raw string
	err := s.db.WithConn(context.Background(), func(conn *zsqlite.Conn) error {
		return sqlitex.ExecuteTransient(conn, `
			SELECT snapshot_json
			FROM sessions
			WHERE workspace_id = ? AND id = ?
		`, &sqlitex.ExecOptions{
			Args: []any{s.workspaceID, id},
			ResultFunc: func(stmt *zsqlite.Stmt) error {
				raw = stmt.ColumnText(0)
				return nil
			},
		})
	})
	if err != nil {
		return nil, err
	}
	if raw == "" {
		return nil, os.ErrNotExist
	}
	var snap Snapshot
	if err := json.Unmarshal([]byte(raw), &snap); err != nil {
		return nil, err
	}
	return &snap, nil
}

func (s *SQLiteStore) Save(id string, snap *Snapshot) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("session: nil sqlite store")
	}
	if snap == nil {
		return fmt.Errorf("session: nil snapshot")
	}
	data, err := json.Marshal(snap)
	if err != nil {
		return err
	}
	createdAt := snap.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now()
	}
	updatedAt := snap.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = createdAt
	}
	title := snapshotTitle(snap)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	return s.db.Tx(context.Background(), func(conn *zsqlite.Conn) error {
		if err := sqlitex.ExecuteTransient(conn, `
			INSERT INTO workspaces (id, path, name, kind, status, metadata_json, created_at, updated_at)
			VALUES (?, ?, ?, 'local', 'active', '{}', ?, ?)
			ON CONFLICT(id) DO UPDATE SET
				path = excluded.path,
				name = excluded.name,
				updated_at = excluded.updated_at
		`, &sqlitex.ExecOptions{
			Args: []any{
				s.workspaceID,
				s.workspacePath,
				s.workspaceName,
				now,
				now,
			},
		}); err != nil {
			return err
		}
		return sqlitex.ExecuteTransient(conn, `
			INSERT INTO sessions (id, workspace_id, title, snapshot_json, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET
				workspace_id = excluded.workspace_id,
				title = excluded.title,
				snapshot_json = excluded.snapshot_json,
				created_at = excluded.created_at,
				updated_at = excluded.updated_at
		`, &sqlitex.ExecOptions{
			Args: []any{
				id,
				s.workspaceID,
				title,
				string(data),
				createdAt.UTC().Format(time.RFC3339Nano),
				updatedAt.UTC().Format(time.RFC3339Nano),
			},
		})
	})
}

func (s *SQLiteStore) Delete(id string) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("session: nil sqlite store")
	}
	return s.db.WithConn(context.Background(), func(conn *zsqlite.Conn) error {
		return sqlitex.ExecuteTransient(conn, `
			DELETE FROM sessions
			WHERE workspace_id = ? AND id = ?
		`, &sqlitex.ExecOptions{
			Args: []any{s.workspaceID, id},
		})
	})
}

func (s *SQLiteStore) List() ([]string, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("session: nil sqlite store")
	}
	var ids []string
	err := s.db.WithConn(context.Background(), func(conn *zsqlite.Conn) error {
		return sqlitex.ExecuteTransient(conn, `
			SELECT id
			FROM sessions
			WHERE workspace_id = ?
			ORDER BY updated_at DESC, id DESC
		`, &sqlitex.ExecOptions{
			Args: []any{s.workspaceID},
			ResultFunc: func(stmt *zsqlite.Stmt) error {
				ids = append(ids, stmt.ColumnText(0))
				return nil
			},
		})
	})
	return ids, err
}

func snapshotTitle(snap *Snapshot) string {
	if snap == nil {
		return ""
	}
	for _, entry := range snap.Entries {
		if entry.Message.Role == "user" {
			text := strings.TrimSpace(entry.Message.Content)
			if text != "" {
				return compactTitle(text, 48)
			}
		}
	}
	return snap.ID
}

func compactTitle(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return string(r)
	}
	if n <= 1 {
		return "…"
	}
	return string(r[:n-1]) + "…"
}

func workspaceID(path string) string {
	if path == "" {
		return "ws_default"
	}
	sum := sha256.Sum256([]byte(path))
	return "ws_" + hex.EncodeToString(sum[:])[:12]
}

func workspaceName(path string) string {
	if path == "" {
		return "workspace"
	}
	name := filepath.Base(path)
	if name == "." || name == string(filepath.Separator) || name == "" {
		return "workspace"
	}
	return name
}
