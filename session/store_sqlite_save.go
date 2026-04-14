package session

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	zsqlite "zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

func (s *SQLiteStore) Save(id string, snap *Snapshot) error {
	if s == nil || s.db == nil {
		return s.errNil()
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
		if err := s.saveWorkspace(conn, now); err != nil {
			return err
		}
		if err := s.saveSession(conn, id, kind, title, status, parentID, forkFromEntrySeq, summary, metadata, string(data), createdAt, updatedAt, closedAt); err != nil {
			return err
		}
		return s.saveEntries(conn, id, snap.Entries, updatedAt)
	})
}

func (s *SQLiteStore) saveWorkspace(conn *zsqlite.Conn, now string) error {
	return sqlitex.ExecuteTransient(conn, `
		INSERT INTO workspaces (id, path, name, kind, status, metadata_json, created_at, updated_at)
		VALUES (?, ?, ?, 'local', 'active', '{}', ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			path = excluded.path,
			name = excluded.name,
			updated_at = excluded.updated_at
	`, &sqlitex.ExecOptions{
		Args: []any{s.workspaceID, s.workspacePath, s.workspaceName, now, now},
	})
}

func (s *SQLiteStore) saveSession(conn *zsqlite.Conn, id, kind, title, status, parentID string, forkFromEntrySeq int, summary, metadata, snapshot string, createdAt, updatedAt time.Time, closedAt any) error {
	return sqlitex.ExecuteTransient(conn, `
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
			snapshot,
			createdAt.UTC().Format(time.RFC3339Nano),
			updatedAt.UTC().Format(time.RFC3339Nano),
			closedAt,
		},
	})
}

func (s *SQLiteStore) saveEntries(conn *zsqlite.Conn, id string, entries []Entry, updatedAt time.Time) error {
	if err := sqlitex.ExecuteTransient(conn, `
		DELETE FROM session_entries
		WHERE session_id = ?
	`, &sqlitex.ExecOptions{
		Args: []any{id},
	}); err != nil {
		return err
	}
	for i, entry := range entries {
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
}
