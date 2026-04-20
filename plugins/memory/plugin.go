package memory

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/abcdlsj/mink/app"
	"github.com/abcdlsj/mink/command"
	_ "modernc.org/sqlite"
)

type scope struct {
	Kind string
	Key  string
}

type doc struct {
	ID        string
	ScopeKind string
	ScopeKey  string
	Title     string
	Body      string
	Summary   string
	Kind      string
	Tags      string
	Source    string
	CreatedAt time.Time
}

type store struct {
	sql       *sql.DB
	workspace string
}

type readArgs struct {
	ScopeKind string `json:"scope_kind"`
	ScopeKey  string `json:"scope_key"`
	Limit     int    `json:"limit"`
}

type searchArgs struct {
	Query     string `json:"query"`
	ScopeKind string `json:"scope_kind"`
	ScopeKey  string `json:"scope_key"`
	Limit     int    `json:"limit"`
}

type writeArgs struct {
	ScopeKind string   `json:"scope_kind"`
	ScopeKey  string   `json:"scope_key"`
	Title     string   `json:"title"`
	Body      string   `json:"body"`
	Summary   string   `json:"summary"`
	Kind      string   `json:"kind"`
	Tags      []string `json:"tags"`
}

const usageText = "usage: !memory [recent [scope] [limit] | search [scope] <query> | save [scope] <title> :: <body>]"
const searchUsageText = "usage: !memory search [scope] <query>"
const saveUsageText = "usage: !memory save [scope] <title> :: <body>"

func Plugin() app.Plugin {
	return func(a *app.App) error {
		path := filepath.Join(filepath.Dir(a.Config().DBPath), "memory.db")
		s, err := open(path, a.Workspace())
		if err != nil {
			return err
		}
		a.RegisterTool(&readTool{s: s})
		a.RegisterTool(&searchTool{s: s})
		a.RegisterTool(&writeTool{s: s})
		a.RegisterCommand(&cmd{s: s})
		return nil
	}
}

func open(path, workspace string) (*store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	s := &store{sql: db, workspace: workspace}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS docs (
		id TEXT PRIMARY KEY,
		scope_kind TEXT NOT NULL,
		scope_key TEXT NOT NULL,
		title TEXT NOT NULL,
		body TEXT NOT NULL,
		summary TEXT NOT NULL,
		kind TEXT NOT NULL,
		tags TEXT NOT NULL,
		source TEXT NOT NULL,
		created_at TEXT NOT NULL
	)`); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *store) put(ctx context.Context, sc scope, d doc) (doc, error) {
	if strings.TrimSpace(d.ID) == "" {
		d.ID = fmt.Sprintf("mem-%d", time.Now().UnixNano())
	}
	if d.CreatedAt.IsZero() {
		d.CreatedAt = time.Now()
	}
	if _, err := s.sql.ExecContext(ctx, `
		INSERT INTO docs (id, scope_kind, scope_key, title, body, summary, kind, tags, source, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, d.ID, sc.Kind, sc.Key, d.Title, d.Body, d.Summary, blank(d.Kind, "note"), d.Tags, d.Source, d.CreatedAt.UTC().Format(time.RFC3339Nano)); err != nil {
		return doc{}, err
	}
	d.ScopeKind = sc.Kind
	d.ScopeKey = sc.Key
	return d, nil
}

func (s *store) recent(ctx context.Context, sc scope, limit int) ([]doc, error) {
	rows, err := s.sql.QueryContext(ctx, `
		SELECT id, scope_kind, scope_key, title, body, summary, kind, tags, source, created_at
		FROM docs WHERE scope_kind = ? AND scope_key = ?
		ORDER BY created_at DESC LIMIT ?
	`, sc.Kind, sc.Key, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scan(rows)
}

func (s *store) search(ctx context.Context, scopes []scope, q string, limit int) ([]doc, error) {
	q = "%" + strings.TrimSpace(q) + "%"
	var out []doc
	seen := map[string]bool{}
	for _, sc := range scopes {
		rows, err := s.sql.QueryContext(ctx, `
			SELECT id, scope_kind, scope_key, title, body, summary, kind, tags, source, created_at
			FROM docs
			WHERE scope_kind = ? AND scope_key = ? AND (title LIKE ? OR body LIKE ? OR summary LIKE ?)
			ORDER BY created_at DESC LIMIT ?
		`, sc.Kind, sc.Key, q, q, q, limit)
		if err != nil {
			return nil, err
		}
		docs, err := scan(rows)
		rows.Close()
		if err != nil {
			return nil, err
		}
		for _, d := range docs {
			if seen[d.ID] {
				continue
			}
			seen[d.ID] = true
			out = append(out, d)
			if len(out) >= limit {
				return out, nil
			}
		}
	}
	return out, nil
}

func scan(rows *sql.Rows) ([]doc, error) {
	var out []doc
	for rows.Next() {
		var d doc
		var created string
		if err := rows.Scan(&d.ID, &d.ScopeKind, &d.ScopeKey, &d.Title, &d.Body, &d.Summary, &d.Kind, &d.Tags, &d.Source, &created); err != nil {
			return nil, err
		}
		d.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		out = append(out, d)
	}
	return out, rows.Err()
}

func decode[T any](name string, args json.RawMessage, dst *T) error {
	if err := json.Unmarshal(args, dst); err != nil {
		return fmt.Errorf("%s: parse error: %w", name, err)
	}
	return nil
}

type readTool struct{ s *store }

func (t *readTool) Name() string { return "read_memory" }
func (t *readTool) Desc() string { return "Read recent memory docs from a scope" }
func (t *readTool) Schema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{
		"scope_kind": map[string]any{"type": "string"},
		"scope_key":  map[string]any{"type": "string"},
		"limit":      map[string]any{"type": "integer"},
	}}
}
func (t *readTool) Run(ctx context.Context, args json.RawMessage) (string, error) {
	var in readArgs
	if err := decode("read_memory", args, &in); err != nil {
		return "", err
	}
	limit := defaultLimit(in.Limit, 3)
	sc := t.s.resolveReadScope(command.SourceFrom(ctx), in.ScopeKind, in.ScopeKey)
	docs, err := t.s.recent(ctx, sc, limit)
	if err != nil {
		return "", err
	}
	return render("recent", []scope{sc}, docs), nil
}

type searchTool struct{ s *store }

func (t *searchTool) Name() string { return "search_memory" }
func (t *searchTool) Desc() string { return "Search memory docs across scopes" }
func (t *searchTool) Schema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{
		"query":      map[string]any{"type": "string"},
		"scope_kind": map[string]any{"type": "string"},
		"scope_key":  map[string]any{"type": "string"},
		"limit":      map[string]any{"type": "integer"},
	}, "required": []string{"query"}}
}
func (t *searchTool) Run(ctx context.Context, args json.RawMessage) (string, error) {
	var in searchArgs
	if err := decode("search_memory", args, &in); err != nil {
		return "", err
	}
	if strings.TrimSpace(in.Query) == "" {
		return "", fmt.Errorf("query is required")
	}
	limit := defaultLimit(in.Limit, 5)
	scopes := t.s.resolveSearchScopes(command.SourceFrom(ctx), in.ScopeKind, in.ScopeKey)
	docs, err := t.s.search(ctx, scopes, in.Query, limit)
	if err != nil {
		return "", err
	}
	return render("search", scopes, docs), nil
}

type writeTool struct{ s *store }

func (t *writeTool) Name() string { return "write_memory" }
func (t *writeTool) Desc() string { return "Write a memory doc" }
func (t *writeTool) Schema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{
		"scope_kind": map[string]any{"type": "string"},
		"scope_key":  map[string]any{"type": "string"},
		"title":      map[string]any{"type": "string"},
		"body":       map[string]any{"type": "string"},
		"summary":    map[string]any{"type": "string"},
		"kind":       map[string]any{"type": "string"},
		"tags":       map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
	}, "required": []string{"title", "body"}}
}
func (t *writeTool) Run(ctx context.Context, args json.RawMessage) (string, error) {
	var in writeArgs
	if err := decode("write_memory", args, &in); err != nil {
		return "", err
	}
	if strings.TrimSpace(in.Title) == "" || strings.TrimSpace(in.Body) == "" {
		return "", fmt.Errorf("title and body are required")
	}
	sc := t.s.resolveWriteScope(command.SourceFrom(ctx), in.ScopeKind, in.ScopeKey)
	d, err := t.s.put(ctx, sc, doc{
		Title:   strings.TrimSpace(in.Title),
		Body:    strings.TrimSpace(in.Body),
		Summary: blank(in.Summary, summarize(in.Body, 140)),
		Kind:    blank(in.Kind, "note"),
		Tags:    strings.Join(in.Tags, ","),
		Source:  command.SourceFrom(ctx),
	})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("saved memory %s in %s:%s", d.ID, d.ScopeKind, d.ScopeKey), nil
}

type cmd struct{ s *store }

func (c *cmd) Name() string { return "memory" }
func (c *cmd) Desc() string { return "memory lookup and notes (!memory recent|search|save)" }
func (c *cmd) Run(ctx context.Context, args []string) (string, error) {
	if len(args) == 0 {
		return usageText, nil
	}
	switch args[0] {
	case "recent", "read":
		return c.runRecent(ctx, args[1:])
	case "search":
		return c.runSearch(ctx, args[1:])
	case "save", "write":
		return c.runSave(ctx, args[1:])
	default:
		return usageText, nil
	}
}

func (c *cmd) runRecent(ctx context.Context, args []string) (string, error) {
	src := command.SourceFrom(ctx)
	sc, rest := c.consumeScope(src, args)
	limit := 5
	if len(rest) > 0 {
		if n, err := strconv.Atoi(rest[0]); err == nil && n > 0 {
			limit = n
		}
	}
	docs, err := c.s.recent(ctx, sc, limit)
	if err != nil {
		return "", err
	}
	return render("recent", []scope{sc}, docs), nil
}

func (c *cmd) runSearch(ctx context.Context, args []string) (string, error) {
	src := command.SourceFrom(ctx)
	scopes, rest := c.consumeScopes(src, args)
	q := strings.TrimSpace(strings.Join(rest, " "))
	if q == "" {
		return searchUsageText, nil
	}
	docs, err := c.s.search(ctx, scopes, q, 8)
	if err != nil {
		return "", err
	}
	return render("search", scopes, docs), nil
}

func (c *cmd) runSave(ctx context.Context, args []string) (string, error) {
	src := command.SourceFrom(ctx)
	sc, rest := c.consumeScope(src, args)
	raw := strings.TrimSpace(strings.Join(rest, " "))
	title, body, ok := strings.Cut(raw, "::")
	if !ok {
		return saveUsageText, nil
	}
	title = strings.TrimSpace(title)
	body = strings.TrimSpace(body)
	if title == "" || body == "" {
		return saveUsageText, nil
	}
	d, err := c.s.put(ctx, sc, doc{
		Title:   title,
		Body:    body,
		Summary: summarize(body, 140),
		Kind:    "note",
		Source:  src,
	})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("saved memory %s in %s:%s", d.ID, d.ScopeKind, d.ScopeKey), nil
}

func (c *cmd) consumeScope(src string, args []string) (scope, []string) {
	if len(args) > 0 && isScopeToken(args[0]) {
		return parseScope(src, args[0], c.s.workspace), args[1:]
	}
	return c.s.resolveReadScope(src, "", ""), args
}

func (c *cmd) consumeScopes(src string, args []string) ([]scope, []string) {
	if len(args) > 0 && isScopeToken(args[0]) {
		return []scope{parseScope(src, args[0], c.s.workspace)}, args[1:]
	}
	return c.s.resolveSearchScopes(src, "", ""), args
}

func (s *store) resolveReadScope(src, kind, key string) scope {
	if strings.TrimSpace(kind) != "" {
		return s.scope(src, kind, key)
	}
	if strings.TrimSpace(src) != "" {
		return scope{Kind: "channel", Key: strings.TrimSpace(src)}
	}
	if strings.TrimSpace(s.workspace) != "" {
		return scope{Kind: "workspace", Key: strings.TrimSpace(s.workspace)}
	}
	return scope{Kind: "global", Key: ""}
}

func (s *store) resolveWriteScope(src, kind, key string) scope {
	return s.resolveReadScope(src, kind, key)
}

func (s *store) resolveSearchScopes(src, kind, key string) []scope {
	if strings.TrimSpace(kind) != "" {
		return []scope{s.scope(src, kind, key)}
	}
	var out []scope
	if strings.TrimSpace(src) != "" {
		out = append(out, scope{Kind: "channel", Key: strings.TrimSpace(src)})
	}
	if strings.TrimSpace(s.workspace) != "" {
		out = append(out, scope{Kind: "workspace", Key: strings.TrimSpace(s.workspace)})
	}
	out = append(out, scope{Kind: "global", Key: ""})
	return out
}

func (s *store) scope(src, kind, key string) scope {
	return resolveScope(src, kind, key, s.workspace)
}

func resolveScope(src, kind, key, workspace string) scope {
	kind = strings.TrimSpace(kind)
	key = strings.TrimSpace(key)
	switch kind {
	case "global":
		return scope{Kind: "global", Key: ""}
	case "workspace":
		return scope{Kind: "workspace", Key: blank(key, workspace)}
	case "channel":
		return scope{Kind: "channel", Key: blank(key, src)}
	default:
		return scope{Kind: blank(kind, "custom"), Key: key}
	}
}

func parseScope(src, raw, workspace string) scope {
	raw = strings.TrimSpace(raw)
	switch raw {
	case "global":
		return scope{Kind: "global", Key: ""}
	case "workspace":
		return scope{Kind: "workspace", Key: workspace}
	case "channel":
		return scope{Kind: "channel", Key: src}
	default:
		kind, key, ok := strings.Cut(raw, ":")
		if !ok {
			return scope{Kind: "custom", Key: raw}
		}
		return scope{Kind: strings.TrimSpace(kind), Key: strings.TrimSpace(key)}
	}
}

func isScopeToken(s string) bool {
	s = strings.TrimSpace(s)
	return s == "global" || s == "workspace" || s == "channel" || strings.Contains(s, ":")
}

func render(mode string, scopes []scope, docs []doc) string {
	var sb strings.Builder
	sb.WriteString("Memory " + mode)
	if len(scopes) > 0 {
		var parts []string
		for _, sc := range scopes {
			if sc.Key == "" {
				parts = append(parts, sc.Kind)
			} else {
				parts = append(parts, sc.Kind+":"+sc.Key)
			}
		}
		sb.WriteString(" [")
		sb.WriteString(strings.Join(parts, ", "))
		sb.WriteString("]")
	}
	sb.WriteByte('\n')
	if len(docs) == 0 {
		sb.WriteString("no memory docs")
		return sb.String()
	}
	for _, d := range docs {
		line := blank(d.Summary, summarize(d.Body, 160))
		fmt.Fprintf(&sb, "- %s (%s): %s\n", d.Title, scopeText(scope{Kind: d.ScopeKind, Key: d.ScopeKey}), line)
	}
	return strings.TrimSpace(sb.String())
}

func summarize(s string, n int) string {
	s = strings.Join(strings.Fields(strings.TrimSpace(s)), " ")
	rs := []rune(s)
	if len(rs) <= n {
		return s
	}
	if n <= 1 {
		return "…"
	}
	return string(rs[:n-1]) + "…"
}

func blank(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return strings.TrimSpace(s)
}

func defaultLimit(n, fallback int) int {
	if n > 0 {
		return n
	}
	return fallback
}

func scopeText(sc scope) string {
	if sc.Key == "" {
		return sc.Kind
	}
	return sc.Kind + ":" + sc.Key
}
