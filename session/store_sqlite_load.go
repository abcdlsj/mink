package session

import (
	"context"
	"encoding/json"
	"os"
	"time"

	"github.com/abcdlsj/mink/msg"
	zsqlite "zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

func (s *SQLiteStore) Load(id string) (*Snapshot, error) {
	if s == nil || s.db == nil {
		return nil, s.errNil()
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
				raw := stmt.ColumnText(2)
				at, _ := time.Parse(time.RFC3339Nano, stmt.ColumnText(3))
				if err := json.Unmarshal([]byte(raw), &m); err != nil {
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
