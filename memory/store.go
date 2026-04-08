package memory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/abcdlsj/mink/runtime/id"
	rtsqlite "github.com/abcdlsj/mink/runtime/store/sqlite"
	zsqlite "zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

type Doc struct {
	ID        string
	Title     string
	Kind      string
	Tags      []string
	TaskID    string
	RunID     string
	Source    string
	Summary   string
	Body      string
	UpdatedAt time.Time
}

type Store struct {
	root string
	db   *rtsqlite.DB
}

func New(root string, db *rtsqlite.DB) *Store {
	return &Store{root: root, db: db}
}

func (s *Store) Put(ctx context.Context, scope string, doc Doc) (Doc, error) {
	if s == nil || s.db == nil {
		return Doc{}, fmt.Errorf("memory: nil store")
	}
	if strings.TrimSpace(scope) == "" {
		scope = "knowledge"
	}
	doc.ID = nonEmpty(doc.ID, id.Memory())
	doc.Kind = nonEmpty(doc.Kind, "note")
	doc.Title = nonEmpty(strings.TrimSpace(doc.Title), doc.ID)
	doc.Body = strings.TrimSpace(doc.Body)
	if doc.Body == "" {
		return Doc{}, fmt.Errorf("memory: empty body")
	}
	if doc.UpdatedAt.IsZero() {
		doc.UpdatedAt = time.Now().UTC()
	}

	path := filepath.Join(s.root, scope, doc.ID+".md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return Doc{}, err
	}
	if err := os.WriteFile(path, []byte(render(doc)), 0o644); err != nil {
		return Doc{}, err
	}
	if err := s.index(ctx, path, doc); err != nil {
		return Doc{}, err
	}
	return doc, nil
}

func (s *Store) Sync(ctx context.Context) error {
	if s == nil || s.db == nil {
		return nil
	}
	if _, err := os.Stat(s.root); os.IsNotExist(err) {
		return nil
	}
	return filepath.WalkDir(s.root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d == nil || d.IsDir() || filepath.Ext(path) != ".md" {
			return err
		}
		return s.SyncPath(ctx, path)
	})
}

func (s *Store) SyncPath(ctx context.Context, path string) error {
	if s == nil || s.db == nil || filepath.Ext(path) != ".md" {
		return nil
	}
	doc, err := s.loadPath(path)
	if err != nil {
		return err
	}
	return s.index(ctx, path, doc)
}

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

func (s *Store) Search(ctx context.Context, query string, limit int) ([]Doc, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("memory: nil store")
	}
	if strings.TrimSpace(query) == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 10
	}
	query = sanitizeMatchQuery(query)
	if query == "" {
		return nil, nil
	}

	var docs []Doc
	err := s.db.WithConn(ctx, func(conn *zsqlite.Conn) error {
		return sqlitex.ExecuteTransient(conn, `
			SELECT d.id, d.title, d.kind, d.tags_json, d.task_id, d.run_id, d.source, d.summary, d.body, d.updated_at
			FROM memory_docs_fts f
			JOIN memory_docs d ON d.rowid = f.rowid
			WHERE memory_docs_fts MATCH ?
			ORDER BY bm25(memory_docs_fts), d.updated_at DESC
			LIMIT ?
		`, &sqlitex.ExecOptions{
			Args: []any{query, limit},
			ResultFunc: func(stmt *zsqlite.Stmt) error {
				docs = append(docs, Doc{
					ID:      stmt.ColumnText(0),
					Title:   stmt.ColumnText(1),
					Kind:    stmt.ColumnText(2),
					Tags:    splitTags(stmt.ColumnText(3)),
					TaskID:  stmt.ColumnText(4),
					RunID:   stmt.ColumnText(5),
					Source:  stmt.ColumnText(6),
					Summary: stmt.ColumnText(7),
					Body:    stmt.ColumnText(8),
				})
				return nil
			},
		})
	})
	return docs, err
}

func (s *Store) RecentByTask(ctx context.Context, taskID string, limit int) ([]Doc, error) {
	if s == nil || s.db == nil || taskID == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 3
	}
	var docs []Doc
	err := s.db.WithConn(ctx, func(conn *zsqlite.Conn) error {
		return sqlitex.ExecuteTransient(conn, `
			SELECT id, title, kind, tags_json, task_id, run_id, source, summary, body, updated_at
			FROM memory_docs
			WHERE task_id = ?
			ORDER BY updated_at DESC
			LIMIT ?
		`, &sqlitex.ExecOptions{
			Args: []any{taskID, limit},
			ResultFunc: func(stmt *zsqlite.Stmt) error {
				updatedAt, _ := time.Parse(time.RFC3339Nano, stmt.ColumnText(9))
				docs = append(docs, Doc{
					ID:        stmt.ColumnText(0),
					Title:     stmt.ColumnText(1),
					Kind:      stmt.ColumnText(2),
					Tags:      splitTags(stmt.ColumnText(3)),
					TaskID:    stmt.ColumnText(4),
					RunID:     stmt.ColumnText(5),
					Source:    stmt.ColumnText(6),
					Summary:   stmt.ColumnText(7),
					Body:      stmt.ColumnText(8),
					UpdatedAt: updatedAt,
				})
				return nil
			},
		})
	})
	return docs, err
}

func (s *Store) RecentBySource(ctx context.Context, source string, limit int) ([]Doc, error) {
	if s == nil || s.db == nil || source == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 3
	}
	var docs []Doc
	err := s.db.WithConn(ctx, func(conn *zsqlite.Conn) error {
		return sqlitex.ExecuteTransient(conn, `
			SELECT id, title, kind, tags_json, task_id, run_id, source, summary, body, updated_at
			FROM memory_docs
			WHERE source = ?
			ORDER BY updated_at DESC
			LIMIT ?
		`, &sqlitex.ExecOptions{
			Args: []any{source, limit},
			ResultFunc: func(stmt *zsqlite.Stmt) error {
				updatedAt, _ := time.Parse(time.RFC3339Nano, stmt.ColumnText(9))
				docs = append(docs, Doc{
					ID:        stmt.ColumnText(0),
					Title:     stmt.ColumnText(1),
					Kind:      stmt.ColumnText(2),
					Tags:      splitTags(stmt.ColumnText(3)),
					TaskID:    stmt.ColumnText(4),
					RunID:     stmt.ColumnText(5),
					Source:    stmt.ColumnText(6),
					Summary:   stmt.ColumnText(7),
					Body:      stmt.ColumnText(8),
					UpdatedAt: updatedAt,
				})
				return nil
			},
		})
	})
	return docs, err
}

func (s *Store) index(ctx context.Context, path string, doc Doc) error {
	tags := joinTags(doc.Tags)
	updatedAt := doc.UpdatedAt.UTC().Format(time.RFC3339Nano)
	indexedAt := time.Now().UTC().Format(time.RFC3339Nano)
	sum := hash(doc.Body)

	if err := s.db.Tx(ctx, func(conn *zsqlite.Conn) error {
		if err := sqlitex.ExecuteTransient(conn, `
			INSERT INTO memory_docs (
				id, path, title, kind, tags_json, task_id, run_id, source,
				summary, body, sha256, updated_at, indexed_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(path) DO UPDATE SET
				id = excluded.id,
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
				doc.ID, path, doc.Title, doc.Kind, tags, nullable(doc.TaskID), nullable(doc.RunID),
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

		if err := sqlitex.ExecuteTransient(conn, `
			INSERT OR REPLACE INTO memory_docs_fts(rowid, title, summary, body)
			VALUES(?, ?, ?, ?)
			`, &sqlitex.ExecOptions{
			Args: []any{rowid, doc.Title, doc.Summary, doc.Body},
		}); err != nil {
			return err
		}
		return nil
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
				"scope": filepath.Base(filepath.Dir(path)),
			},
		})
	}
	return nil
}

func render(doc Doc) string {
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "id: %s\n", doc.ID)
	fmt.Fprintf(&b, "title: %s\n", escape(doc.Title))
	fmt.Fprintf(&b, "kind: %s\n", escape(doc.Kind))
	b.WriteString("tags:\n")
	for _, tag := range doc.Tags {
		fmt.Fprintf(&b, "  - %s\n", escape(tag))
	}
	if doc.TaskID != "" {
		fmt.Fprintf(&b, "task_id: %s\n", doc.TaskID)
	}
	if doc.RunID != "" {
		fmt.Fprintf(&b, "run_id: %s\n", doc.RunID)
	}
	if doc.Source != "" {
		fmt.Fprintf(&b, "source: %s\n", escape(doc.Source))
	}
	if doc.Summary != "" {
		fmt.Fprintf(&b, "summary: %s\n", escape(doc.Summary))
	}
	fmt.Fprintf(&b, "updated_at: %s\n", doc.UpdatedAt.UTC().Format(time.RFC3339Nano))
	b.WriteString("---\n\n")
	b.WriteString(doc.Body)
	if !strings.HasSuffix(doc.Body, "\n") {
		b.WriteByte('\n')
	}
	return b.String()
}

func (s *Store) loadPath(path string) (Doc, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Doc{}, err
	}
	doc := Doc{
		ID:    strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)),
		Title: strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)),
		Kind:  "note",
		Body:  strings.TrimSpace(string(data)),
	}
	if info, err := os.Stat(path); err == nil {
		doc.UpdatedAt = info.ModTime().UTC()
	}
	if strings.HasPrefix(string(data), "---\n") {
		rest := string(data[4:])
		if i := strings.Index(rest, "\n---\n"); i >= 0 {
			parseFrontmatter(&doc, rest[:i])
			doc.Body = strings.TrimSpace(rest[i+5:])
		}
	}
	doc.Title = nonEmpty(strings.TrimSpace(doc.Title), doc.ID)
	doc.Kind = nonEmpty(strings.TrimSpace(doc.Kind), "note")
	if doc.Body == "" {
		return Doc{}, fmt.Errorf("memory: empty body")
	}
	return doc, nil
}

func joinTags(tags []string) string {
	if len(tags) == 0 {
		return ""
	}
	return strings.Join(tags, ",")
}

func splitTags(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := parts[:0]
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func parseFrontmatter(doc *Doc, head string) {
	lines := strings.Split(head, "\n")
	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		if line == "tags:" {
			for i+1 < len(lines) {
				next := strings.TrimSpace(lines[i+1])
				if !strings.HasPrefix(next, "- ") {
					break
				}
				doc.Tags = append(doc.Tags, unquote(strings.TrimSpace(strings.TrimPrefix(next, "- "))))
				i++
			}
			continue
		}
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		val = unquote(strings.TrimSpace(val))
		switch strings.TrimSpace(key) {
		case "id":
			doc.ID = nonEmpty(val, doc.ID)
		case "title":
			doc.Title = val
		case "kind":
			doc.Kind = val
		case "task_id":
			doc.TaskID = val
		case "run_id":
			doc.RunID = val
		case "source":
			doc.Source = val
		case "summary":
			doc.Summary = val
		case "updated_at":
			if ts, err := time.Parse(time.RFC3339Nano, val); err == nil {
				doc.UpdatedAt = ts
			}
		}
	}
}

func escape(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if strings.ContainsAny(s, ":#[]{}\",'") {
		return fmt.Sprintf("%q", s)
	}
	return s
}

func unquote(s string) string {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		if v, err := strconv.Unquote(s); err == nil {
			return v
		}
	}
	return s
}

func nonEmpty(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

func hash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func sanitizeMatchQuery(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	terms := strings.FieldsFunc(s, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	})
	if len(terms) == 0 {
		return ""
	}
	quoted := make([]string, 0, len(terms))
	for _, term := range terms {
		term = strings.TrimSpace(term)
		if term == "" {
			continue
		}
		quoted = append(quoted, `"`+strings.ReplaceAll(term, `"`, `""`)+`"`)
	}
	return strings.Join(quoted, " AND ")
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}
