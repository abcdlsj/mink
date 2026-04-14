package memory

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/abcdlsj/mink/internal/xstr"
	"github.com/abcdlsj/mink/runtime/id"
	rtsqlite "github.com/abcdlsj/mink/runtime/store/sqlite"
)

type Doc struct {
	ID        string
	Workspace string
	Scope     Scope
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
	return s.PutScoped(ctx, ParseScope(scope), doc)
}

func (s *Store) PutScoped(ctx context.Context, scope Scope, doc Doc) (Doc, error) {
	if s == nil || s.db == nil {
		return Doc{}, fmt.Errorf("memory: nil store")
	}
	doc.Workspace = nonEmpty(strings.TrimSpace(doc.Workspace), s.workspaceID())
	doc.Scope = normalizeScope(scope)
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

	path := filepath.Join(doc.Scope.Dir(s.scopeRoot()), doc.ID+".md")
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
	root := s.scopeRoot()
	if _, err := os.Stat(root); os.IsNotExist(err) {
		return nil
	}
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
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

var nonEmpty = xstr.NonEmpty

func (s *Store) workspaceID() string {
	if s == nil || s.db == nil {
		return "ws_default"
	}
	return s.db.WorkspaceID()
}

func (s *Store) scopeRoot() string {
	if s == nil {
		return ""
	}
	return filepath.Join(s.root, "workspaces", s.workspaceID())
}

var nullable = xstr.Nullable
