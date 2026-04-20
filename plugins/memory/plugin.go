package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/abcdlsj/mink/app"
	"github.com/abcdlsj/mink/command"
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
	Tags      []string
	Source    string
	Path      string
	CreatedAt time.Time
	UpdatedAt time.Time
}

type store struct {
	root      string
	workspace string
	mu        sync.Mutex
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
		s, err := open(a.Config().MemoryDir(), a.Workspace())
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

func open(root, workspace string) (*store, error) {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	return &store{root: root, workspace: workspace}, nil
}

func (s *store) put(ctx context.Context, sc scope, d doc) (doc, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if strings.TrimSpace(d.ID) == "" {
		d.ID = fmt.Sprintf("mem-%d", time.Now().UnixNano())
	}
	now := time.Now().UTC()
	if d.CreatedAt.IsZero() {
		d.CreatedAt = now
	}
	d.UpdatedAt = now
	d.ScopeKind = sc.Kind
	d.ScopeKey = sc.Key
	if strings.TrimSpace(d.Kind) == "" {
		d.Kind = "note"
	}
	if strings.TrimSpace(d.Summary) == "" {
		d.Summary = summarize(d.Body, 140)
	}
	path := filepath.Join(scopeDir(s.root, sc), slug(d.Title, d.ID)+".md")
	d.Path = path
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return doc{}, err
	}
	if err := writeFile(path, []byte(renderDoc(d))); err != nil {
		return doc{}, err
	}
	return d, nil
}

func (s *store) recent(ctx context.Context, sc scope, limit int) ([]doc, error) {
	docs, err := s.loadScope(sc)
	if err != nil {
		return nil, err
	}
	sort.Slice(docs, func(i, j int) bool {
		return docs[i].UpdatedAt.After(docs[j].UpdatedAt)
	})
	if len(docs) > limit {
		docs = docs[:limit]
	}
	return docs, nil
}

func (s *store) search(ctx context.Context, scopes []scope, q string, limit int) ([]doc, error) {
	q = strings.ToLower(strings.TrimSpace(q))
	if q == "" {
		return nil, nil
	}
	var out []doc
	seen := map[string]bool{}
	for _, sc := range scopes {
		docs, err := s.loadScope(sc)
		if err != nil {
			return nil, err
		}
		sort.Slice(docs, func(i, j int) bool {
			return docs[i].UpdatedAt.After(docs[j].UpdatedAt)
		})
		for _, d := range docs {
			if seen[d.Path] {
				continue
			}
			text := strings.ToLower(strings.Join([]string{d.Title, d.Summary, d.Body}, "\n"))
			if !strings.Contains(text, q) {
				continue
			}
			seen[d.Path] = true
			out = append(out, d)
			if len(out) >= limit {
				return out, nil
			}
		}
	}
	return out, nil
}

func (s *store) loadScope(sc scope) ([]doc, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	root := scopeDir(s.root, sc)
	if _, err := os.Stat(root); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []doc
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || filepath.Ext(path) != ".md" {
			return nil
		}
		doc, err := loadDoc(path, sc)
		if err != nil {
			return err
		}
		out = append(out, doc)
		return nil
	})
	return out, err
}

func loadDoc(path string, sc scope) (doc, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return doc{}, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return doc{}, err
	}
	body := string(data)
	head := ""
	if strings.HasPrefix(body, "---\n") {
		rest := body[4:]
		if i := strings.Index(rest, "\n---\n"); i >= 0 {
			head = rest[:i]
			body = rest[i+5:]
		}
	}
	d := doc{
		ID:        strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)),
		ScopeKind: sc.Kind,
		ScopeKey:  sc.Key,
		Path:      path,
		Body:      strings.TrimSpace(body),
		CreatedAt: info.ModTime().UTC(),
		UpdatedAt: info.ModTime().UTC(),
		Kind:      "note",
	}
	parseFrontmatter(&d, head)
	if d.Title == "" {
		d.Title = firstHeading(d.Body)
	}
	if d.Title == "" {
		d.Title = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}
	if d.Summary == "" {
		d.Summary = summarize(d.Body, 160)
	}
	return d, nil
}

func renderDoc(d doc) string {
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "title: %s\n", quote(d.Title))
	fmt.Fprintf(&b, "kind: %s\n", quote(d.Kind))
	if d.Source != "" {
		fmt.Fprintf(&b, "source: %s\n", quote(d.Source))
	}
	if d.Summary != "" {
		fmt.Fprintf(&b, "summary: %s\n", quote(d.Summary))
	}
	if len(d.Tags) > 0 {
		b.WriteString("tags:\n")
		for _, tag := range d.Tags {
			fmt.Fprintf(&b, "  - %s\n", quote(tag))
		}
	}
	fmt.Fprintf(&b, "updated_at: %s\n", d.UpdatedAt.Format(time.RFC3339Nano))
	b.WriteString("---\n\n")
	b.WriteString("# ")
	b.WriteString(strings.TrimSpace(d.Title))
	b.WriteString("\n\n")
	b.WriteString(strings.TrimSpace(d.Body))
	if !strings.HasSuffix(d.Body, "\n") {
		b.WriteByte('\n')
	}
	return b.String()
}

func parseFrontmatter(d *doc, head string) {
	if strings.TrimSpace(head) == "" {
		return
	}
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
				d.Tags = append(d.Tags, unquote(strings.TrimSpace(strings.TrimPrefix(next, "- "))))
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
		case "title":
			d.Title = val
		case "kind":
			d.Kind = val
		case "source":
			d.Source = val
		case "summary":
			d.Summary = val
		case "updated_at":
			if ts, err := time.Parse(time.RFC3339Nano, val); err == nil {
				d.UpdatedAt = ts
			}
		}
	}
}

func firstHeading(body string) string {
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") {
			return strings.TrimSpace(strings.TrimLeft(line, "#"))
		}
	}
	return ""
}

func quote(s string) string {
	s = strings.ReplaceAll(strings.TrimSpace(s), "\n", " ")
	return strconv.Quote(s)
}

func unquote(s string) string {
	if v, err := strconv.Unquote(s); err == nil {
		return v
	}
	return s
}

func scopeDir(root string, sc scope) string {
	sc = normalizeScope(sc)
	if sc.Key == "" {
		return filepath.Join(root, sc.Kind)
	}
	return filepath.Join(root, sc.Kind, sanitize(sc.Key))
}

func sanitize(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "_"
	}
	r := strings.NewReplacer("/", "_", "\\", "_", ":", "_", "\n", "_", "\t", "_")
	return r.Replace(s)
}

func slug(title, fallback string) string {
	title = strings.ToLower(strings.TrimSpace(title))
	if title == "" {
		return fallback
	}
	var b strings.Builder
	lastDash := false
	for _, r := range title {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		case !lastDash:
			b.WriteByte('-')
			lastDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return fallback
	}
	return out
}

func writeFile(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	name := f.Name()
	if _, err := f.Write(data); err != nil {
		f.Close()
		_ = os.Remove(name)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		_ = os.Remove(name)
		return err
	}
	return nil
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
		Tags:    in.Tags,
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

func normalizeScope(sc scope) scope {
	sc.Kind = strings.TrimSpace(sc.Kind)
	sc.Key = strings.TrimSpace(sc.Key)
	if sc.Kind == "" {
		sc.Kind = "global"
	}
	return sc
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
			parts = append(parts, scopeText(sc))
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
	if n <= 3 {
		return "..."
	}
	return string(rs[:n-3]) + "..."
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
