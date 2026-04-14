package memory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"time"

	rtsqlite "github.com/abcdlsj/mink/runtime/store/sqlite"
	zsqlite "zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

func (s *Store) DeletePath(ctx context.Context, path string) error {
	if s == nil || s.db == nil || path == "" {
		return nil
	}

	var rowid int64
	var doc Doc
	err := s.db.Tx(ctx, func(conn *zsqlite.Conn) error {
		if err := sqlitex.ExecuteTransient(conn, `
			SELECT rowid, id, task_id, run_id, source
			FROM memory_docs
			WHERE path = ?
		`, &sqlitex.ExecOptions{
			Args: []any{path},
			ResultFunc: func(stmt *zsqlite.Stmt) error {
				rowid = stmt.ColumnInt64(0)
				doc.ID = stmt.ColumnText(1)
				doc.TaskID = stmt.ColumnText(2)
				doc.RunID = stmt.ColumnText(3)
				doc.Source = stmt.ColumnText(4)
				return nil
			},
		}); err != nil {
			return err
		}
		if rowid != 0 {
			if err := sqlitex.ExecuteTransient(conn, `
				DELETE FROM memory_docs_fts
				WHERE rowid = ?
			`, &sqlitex.ExecOptions{
				Args: []any{rowid},
			}); err != nil {
				return err
			}
			if err := sqlitex.ExecuteTransient(conn, `
				DELETE FROM memory_docs
				WHERE path = ?
			`, &sqlitex.ExecOptions{
				Args: []any{path},
			}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}

	if doc.TaskID != "" {
		_ = s.db.AppendEvent(ctx, rtsqlite.Event{
			TaskID:    doc.TaskID,
			RunID:     doc.RunID,
			Type:      "memory.doc_deleted",
			ActorType: "system",
			ActorID:   "memory",
			Source:    doc.Source,
			Payload: map[string]any{
				"id":   doc.ID,
				"path": path,
			},
		})
	}
	return nil
}

func (s *Store) index(ctx context.Context, path string, doc Doc) error {
	tags := joinTags(doc.Tags)
	updatedAt := doc.UpdatedAt.UTC().Format(time.RFC3339Nano)
	indexedAt := time.Now().UTC().Format(time.RFC3339Nano)
	sum := hash(doc.Body)
	scope := normalizeScope(doc.Scope)

	if err := s.db.Tx(ctx, func(conn *zsqlite.Conn) error {
		if err := sqlitex.ExecuteTransient(conn, `
			INSERT INTO memory_docs (
				id, path, workspace_id, scope_kind, scope_key, title, kind, tags_json, task_id, run_id, source,
				summary, body, sha256, updated_at, indexed_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(path) DO UPDATE SET
				id = excluded.id,
				workspace_id = excluded.workspace_id,
				scope_kind = excluded.scope_kind,
				scope_key = excluded.scope_key,
				title = excluded.title,
				kind = excluded.kind,
				tags_json = excluded.tags_json,
				task_id = excluded.task_id,
				run_id = excluded.run_id,
				source = excluded.source,
				summary = excluded.summary,
				body = excluded.body,
				sha256 = excluded.sha256,
				updated_at = excluded.updated_at,
				indexed_at = excluded.indexed_at
		`, &sqlitex.ExecOptions{
			Args: []any{
				doc.ID, path, doc.Workspace, scope.Kind, scope.Key, doc.Title, doc.Kind, tags, nullable(doc.TaskID), nullable(doc.RunID),
				doc.Source, doc.Summary, doc.Body, sum, updatedAt, indexedAt,
			},
		}); err != nil {
			return err
		}

		var rowid int64
		if err := sqlitex.ExecuteTransient(conn, `
			SELECT rowid FROM memory_docs WHERE path = ?
		`, &sqlitex.ExecOptions{
			Args: []any{path},
			ResultFunc: func(stmt *zsqlite.Stmt) error {
				rowid = stmt.ColumnInt64(0)
				return nil
			},
		}); err != nil {
			return err
		}

		return sqlitex.ExecuteTransient(conn, `
			INSERT OR REPLACE INTO memory_docs_fts(rowid, title, summary, body)
			VALUES(?, ?, ?, ?)
		`, &sqlitex.ExecOptions{
			Args: []any{rowid, doc.Title, doc.Summary, doc.Body},
		})
	}); err != nil {
		return err
	}

	if doc.TaskID != "" {
		_ = s.db.AppendEvent(ctx, rtsqlite.Event{
			TaskID:    doc.TaskID,
			RunID:     doc.RunID,
			Type:      "memory.doc_indexed",
			ActorType: "system",
			ActorID:   "memory",
			Source:    doc.Source,
			Payload: map[string]any{
				"id":    doc.ID,
				"path":  path,
				"scope": doc.Scope.String(),
			},
		})
	}
	return nil
}

func hash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func scanDoc(stmt *zsqlite.Stmt) Doc {
	updatedAt, _ := time.Parse(time.RFC3339Nano, stmt.ColumnText(12))
	return Doc{
		ID:        stmt.ColumnText(0),
		Workspace: stmt.ColumnText(1),
		Scope:     normalizeScope(Scope{Kind: stmt.ColumnText(2), Key: stmt.ColumnText(3)}),
		Title:     stmt.ColumnText(4),
		Kind:      stmt.ColumnText(5),
		Tags:      splitTags(stmt.ColumnText(6)),
		TaskID:    stmt.ColumnText(7),
		RunID:     stmt.ColumnText(8),
		Source:    stmt.ColumnText(9),
		Summary:   stmt.ColumnText(10),
		Body:      stmt.ColumnText(11),
		UpdatedAt: updatedAt,
	}
}
