package memory

import (
	"context"
	"fmt"
	"strings"
	"unicode"

	zsqlite "zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

func (s *Store) Search(ctx context.Context, query string, limit int) ([]Doc, error) {
	return s.SearchScoped(ctx, nil, query, limit)
}

func (s *Store) SearchScoped(ctx context.Context, scopes []Scope, query string, limit int) ([]Doc, error) {
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

	where, args := scopeWhere(scopes)
	args = append(args, query, limit)

	var docs []Doc
	err := s.db.WithConn(ctx, func(conn *zsqlite.Conn) error {
		q := `
			SELECT d.id, d.workspace_id, d.scope_kind, d.scope_key, d.title, d.kind, d.tags_json, d.task_id, d.run_id, d.source, d.summary, d.body, d.updated_at
			FROM memory_docs_fts f
			JOIN memory_docs d ON d.rowid = f.rowid
			WHERE d.workspace_id = ? AND ` + where + ` AND memory_docs_fts MATCH ?
			ORDER BY bm25(memory_docs_fts), d.updated_at DESC
			LIMIT ?`
		return sqlitex.ExecuteTransient(conn, q, &sqlitex.ExecOptions{
			Args: append([]any{s.workspaceID()}, args...),
			ResultFunc: func(stmt *zsqlite.Stmt) error {
				docs = append(docs, scanDoc(stmt))
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
			SELECT id, workspace_id, scope_kind, scope_key, title, kind, tags_json, task_id, run_id, source, summary, body, updated_at
			FROM memory_docs
			WHERE workspace_id = ? AND task_id = ?
			ORDER BY updated_at DESC
			LIMIT ?
		`, &sqlitex.ExecOptions{
			Args: []any{s.workspaceID(), taskID, limit},
			ResultFunc: func(stmt *zsqlite.Stmt) error {
				docs = append(docs, scanDoc(stmt))
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
			SELECT id, workspace_id, scope_kind, scope_key, title, kind, tags_json, task_id, run_id, source, summary, body, updated_at
			FROM memory_docs
			WHERE workspace_id = ? AND source = ?
			ORDER BY updated_at DESC
			LIMIT ?
		`, &sqlitex.ExecOptions{
			Args: []any{s.workspaceID(), source, limit},
			ResultFunc: func(stmt *zsqlite.Stmt) error {
				docs = append(docs, scanDoc(stmt))
				return nil
			},
		})
	})
	return docs, err
}

func (s *Store) RecentByScope(ctx context.Context, scope Scope, limit int) ([]Doc, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = 3
	}
	scope = normalizeScope(scope)
	var docs []Doc
	err := s.db.WithConn(ctx, func(conn *zsqlite.Conn) error {
		return sqlitex.ExecuteTransient(conn, `
			SELECT id, workspace_id, scope_kind, scope_key, title, kind, tags_json, task_id, run_id, source, summary, body, updated_at
			FROM memory_docs
			WHERE workspace_id = ? AND scope_kind = ? AND scope_key = ?
			ORDER BY updated_at DESC
			LIMIT ?
		`, &sqlitex.ExecOptions{
			Args: []any{s.workspaceID(), scope.Kind, scope.Key, limit},
			ResultFunc: func(stmt *zsqlite.Stmt) error {
				docs = append(docs, scanDoc(stmt))
				return nil
			},
		})
	})
	return docs, err
}

func scopeWhere(scopes []Scope) (string, []any) {
	if len(scopes) == 0 {
		return "1 = 1", nil
	}
	parts := make([]string, 0, len(scopes))
	args := make([]any, 0, len(scopes)*2)
	for _, scope := range scopes {
		scope = normalizeScope(scope)
		parts = append(parts, "(d.scope_kind = ? AND d.scope_key = ?)")
		args = append(args, scope.Kind, scope.Key)
	}
	return "(" + strings.Join(parts, " OR ") + ")", args
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
