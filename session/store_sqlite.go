package session

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/abcdlsj/mink/internal/xstr"
	"github.com/abcdlsj/mink/msg"
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
	ws := rtsqlite.ResolveWorkspace(workspacePath)
	return &SQLiteStore{
		db:            db,
		workspaceID:   ws.ID,
		workspacePath: ws.Path,
		workspaceName: ws.Name,
	}
}

func (s *SQLiteStore) Load(id string) (*Snapshot, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("session: nil sqlite store")
	}
	var row struct {
		Kind         string
		Title        string
		Status       string
		ParentID     string
		ForkEntrySeq int
		Summary      string
		Metadata     string
		Snapshot     string
		CreatedAt    time.Time
		UpdatedAt    time.Time
		ClosedAt     *time.Time
	}
	err := s.db.WithConn(context.Background(), func(conn *zsqlite.Conn) error {
		return sqlitex.ExecuteTransient(conn, `
			SELECT kind, title, status, parent_session_id, fork_from_entry_seq, summary, metadata_json, snapshot_json, created_at, updated_at, closed_at
			FROM sessions
			WHERE workspace_id = ? AND id = ?
		`, &sqlitex.ExecOptions{
			Args: []any{s.workspaceID, id},
			ResultFunc: func(stmt *zsqlite.Stmt) error {
				row.Kind = stmt.ColumnText(0)
				row.Title = stmt.ColumnText(1)
				row.Status = stmt.ColumnText(2)
				row.ParentID = stmt.ColumnText(3)
				row.ForkEntrySeq = int(stmt.ColumnInt64(4))
				row.Summary = stmt.ColumnText(5)
				row.Metadata = stmt.ColumnText(6)
				row.Snapshot = stmt.ColumnText(7)
				row.CreatedAt, _ = time.Parse(time.RFC3339Nano, stmt.ColumnText(8))
				row.UpdatedAt, _ = time.Parse(time.RFC3339Nano, stmt.ColumnText(9))
				if stmt.ColumnType(10) != zsqlite.TypeNull {
					if ts, err := time.Parse(time.RFC3339Nano, stmt.ColumnText(10)); err == nil {
						row.ClosedAt = &ts
					}
				}
				return nil
			},
		})
	})
	if err != nil {
		return nil, err
	}
	if row.Snapshot == "" {
		return nil, os.ErrNotExist
	}
	var snap Snapshot
	if err := json.Unmarshal([]byte(row.Snapshot), &snap); err != nil {
		return nil, err
	}
	snap.Kind = firstNonEmpty(snap.Kind, row.Kind)
	snap.Status = firstNonEmpty(snap.Status, row.Status)
	snap.Summary = firstNonEmpty(snap.Summary, row.Summary)
	if len(snap.Metadata) == 0 && row.Metadata != "" {
		snap.Metadata = json.RawMessage(row.Metadata)
	}
	if snap.CreatedAt.IsZero() {
		snap.CreatedAt = row.CreatedAt
	}
	if snap.UpdatedAt.IsZero() {
		snap.UpdatedAt = row.UpdatedAt
	}
	if snap.ClosedAt == nil {
		snap.ClosedAt = row.ClosedAt
	}
	if snap.Provenance == nil && row.ParentID != "" {
		snap.Provenance = &Provenance{
			ParentSessionID: row.ParentID,
			ForkEntryCount:  row.ForkEntrySeq,
		}
	}
	entries, err := s.loadEntries(id)
	if err != nil {
		return nil, err
	}
	if len(entries) > 0 {
		snap.Entries = entries
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
	data, err := json.Marshal(snapshotMeta(snap))
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
	kind := firstNonEmpty(snap.Kind, "main")
	status := firstNonEmpty(snap.Status, "active")
	summary := strings.TrimSpace(snap.Summary)
	if summary == "" && len(snap.Anchors) > 0 {
		summary = strings.TrimSpace(snap.Anchors[len(snap.Anchors)-1].Summary)
	}
	parentID := ""
	forkFromEntrySeq := 0
	if snap.Provenance != nil {
		parentID = strings.TrimSpace(snap.Provenance.ParentSessionID)
		forkFromEntrySeq = snap.Provenance.ForkEntryCount
	}
	metadata := string(snapshotMetadataJSON(snap.Metadata))
	var closedAt any
	if snap.ClosedAt != nil && !snap.ClosedAt.IsZero() {
		closedAt = snap.ClosedAt.UTC().Format(time.RFC3339Nano)
	}
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
		if err := sqlitex.ExecuteTransient(conn, `
			INSERT INTO sessions (id, workspace_id, kind, title, status, parent_session_id, fork_from_entry_seq, summary, metadata_json, snapshot_json, created_at, updated_at, closed_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET
				workspace_id = excluded.workspace_id,
				kind = excluded.kind,
				title = excluded.title,
				status = excluded.status,
				parent_session_id = excluded.parent_session_id,
				fork_from_entry_seq = excluded.fork_from_entry_seq,
				summary = excluded.summary,
				metadata_json = excluded.metadata_json,
				snapshot_json = excluded.snapshot_json,
				created_at = excluded.created_at,
				updated_at = excluded.updated_at,
				closed_at = excluded.closed_at
		`, &sqlitex.ExecOptions{
			Args: []any{
				id,
				s.workspaceID,
				kind,
				title,
				status,
				parentID,
				forkFromEntrySeq,
				summary,
				metadata,
				string(data),
				createdAt.UTC().Format(time.RFC3339Nano),
				updatedAt.UTC().Format(time.RFC3339Nano),
				closedAt,
			},
		}); err != nil {
			return err
		}
		if err := sqlitex.ExecuteTransient(conn, `
			DELETE FROM session_entries
			WHERE session_id = ?
		`, &sqlitex.ExecOptions{
			Args: []any{id},
		}); err != nil {
			return err
		}
		for i, entry := range snap.Entries {
			rawMsg, err := json.Marshal(entry.Message)
			if err != nil {
				return err
			}
			createdAt := entry.CreatedAt
			if createdAt.IsZero() {
				createdAt = entry.Message.Timestamp
			}
			if createdAt.IsZero() {
				createdAt = updatedAt
			}
			if err := sqlitex.ExecuteTransient(conn, `
				INSERT INTO session_entries (id, session_id, seq, entry_kind, message_json, created_at)
				VALUES (?, ?, ?, ?, ?, ?)
			`, &sqlitex.ExecOptions{
				Args: []any{
					firstNonEmpty(entry.ID, fmt.Sprintf("%s-%d", id, i+1)),
					id,
					i + 1,
					firstNonEmpty(string(entry.Kind), string(entryKind(entry.Message))),
					string(rawMsg),
					createdAt.UTC().Format(time.RFC3339Nano),
				},
			}); err != nil {
				return err
			}
		}
		return nil
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
				return xstr.Truncate(text, 48)
			}
		}
	}
	return snap.ID
}

func snapshotMeta(snap *Snapshot) *Snapshot {
	if snap == nil {
		return nil
	}
	meta := *snap
	meta.Entries = nil
	return &meta
}

func snapshotMetadataJSON(raw json.RawMessage) []byte {
	if len(raw) == 0 {
		return []byte("{}")
	}
	return raw
}

func (s *SQLiteStore) loadEntries(id string) ([]Entry, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	var entries []Entry
	err := s.db.WithConn(context.Background(), func(conn *zsqlite.Conn) error {
		return sqlitex.ExecuteTransient(conn, `
			SELECT id, entry_kind, message_json, created_at
			FROM session_entries
			WHERE session_id = ?
			ORDER BY seq ASC
		`, &sqlitex.ExecOptions{
			Args: []any{id},
			ResultFunc: func(stmt *zsqlite.Stmt) error {
				var m msg.Message
				var msgJSON = stmt.ColumnText(2)
				var at, _ = time.Parse(time.RFC3339Nano, stmt.ColumnText(3))
				if err := json.Unmarshal([]byte(msgJSON), &m); err != nil {
					return err
				}
				entries = append(entries, Entry{
					ID:        stmt.ColumnText(0),
					Kind:      EntryKind(stmt.ColumnText(1)),
					Message:   m,
					CreatedAt: at,
				})
				return nil
			},
		})
	})
	return entries, err
}

var firstNonEmpty = xstr.FirstNonEmpty
