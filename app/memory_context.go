package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/abcdlsj/sumi/agent"
	"github.com/abcdlsj/sumi/command"
)

type MemoryOverview struct {
	Scopes []MemoryScopeOverview `json:"scopes"`
}

type MemoryScopeOverview struct {
	Kind   string              `json:"kind"`
	Key    string              `json:"key,omitempty"`
	Label  string              `json:"label"`
	Recent []MemoryDocOverview `json:"recent"`
}

type MemoryDocOverview struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Summary   string    `json:"summary,omitempty"`
	Kind      string    `json:"kind,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

func externalRuntimeName(name string) bool {
	switch strings.TrimSpace(strings.ToLower(name)) {
	case "claude", "codex":
		return true
	default:
		return false
	}
}

func (a *App) prepareMemoryForTurn(ctx context.Context, turn *agent.Turn, precommitExplicit bool) {
	if a == nil || turn == nil || a.tools == nil {
		return
	}
	if precommitExplicit {
		turn.MemoryNotice = a.commitExplicitRemember(ctx, turn)
	}
	turn.MemoryBrief = a.memoryBrief(ctx)
}

func (a *App) commitExplicitRemember(ctx context.Context, turn *agent.Turn) string {
	input := strings.TrimSpace(command.InputFrom(ctx))
	if input == "" || !explicitRememberRequest(input) {
		return ""
	}
	scopeKind, scopeKey := firstMemoryScope(ctx)
	args := map[string]any{
		"scope_kind":         scopeKind,
		"scope_key":          scopeKey,
		"title":              rememberTitle(input),
		"body":               rememberBody(input),
		"kind":               "preference",
		"authorization_text": input,
		"source_space_id":    strings.TrimSpace(turn.SpaceID),
		"source_message_id":  strings.TrimSpace(turn.ParentMessageID),
		"confidence":         "high",
	}
	raw, _ := json.Marshal(args)
	out, err := a.tools.Run(ctx, "remember_memory", raw)
	if err != nil {
		return "Sumi did not change long-term memory: " + err.Error()
	}
	return strings.TrimSpace(out)
}

func (a *App) memoryBrief(ctx context.Context) string {
	var chunks []string
	seen := map[string]bool{}
	for i, sc := range command.MemoryScopesFrom(ctx) {
		kind := strings.TrimSpace(sc.Kind)
		key := strings.TrimSpace(sc.Key)
		if kind == "" {
			continue
		}
		scopeID := kind + "\x00" + key
		if seen[scopeID] {
			continue
		}
		seen[scopeID] = true
		limit := 3
		if i == 0 {
			limit = 5
		}
		raw, _ := json.Marshal(map[string]any{
			"scope_kind": kind,
			"scope_key":  key,
			"limit":      limit,
		})
		out, err := a.tools.Run(ctx, "read_memory", raw)
		if err != nil {
			continue
		}
		out = strings.TrimSpace(out)
		if out == "" || strings.Contains(out, "no memory docs") {
			continue
		}
		chunks = append(chunks, out)
		if len(chunks) >= 4 {
			break
		}
	}
	return strings.Join(chunks, "\n\n")
}

func (a *App) MemoryOverview(scopes []command.MemoryScope, limit int) MemoryOverview {
	if a == nil {
		return MemoryOverview{Scopes: []MemoryScopeOverview{}}
	}
	if limit <= 0 {
		limit = 3
	}
	out := MemoryOverview{Scopes: make([]MemoryScopeOverview, 0, len(scopes))}
	seen := map[string]bool{}
	for _, sc := range scopes {
		kind := strings.TrimSpace(sc.Kind)
		key := strings.TrimSpace(sc.Key)
		if kind == "" {
			continue
		}
		scopeID := kind + "\x00" + key
		if seen[scopeID] {
			continue
		}
		seen[scopeID] = true
		out.Scopes = append(out.Scopes, MemoryScopeOverview{
			Kind:   kind,
			Key:    memoryScopePublicKey(kind, key),
			Label:  memoryScopeLabel(kind, key),
			Recent: a.recentMemoryDocs(kind, key, limit),
		})
	}
	return out
}

func (a *App) recentMemoryDocs(kind, key string, limit int) []MemoryDocOverview {
	dir := filepath.Join(a.cfg.MemoryDir(), sanitizeMemoryPath(kind))
	if strings.TrimSpace(key) != "" {
		dir = filepath.Join(dir, sanitizeMemoryPath(key))
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return []MemoryDocOverview{}
	}
	docs := make([]MemoryDocOverview, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".md" {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		doc := memoryDocOverviewFromFile(entry.Name(), string(data), info.ModTime().UTC())
		docs = append(docs, doc)
	}
	sort.Slice(docs, func(i, j int) bool {
		return docs[i].UpdatedAt.After(docs[j].UpdatedAt)
	})
	if len(docs) > limit {
		docs = docs[:limit]
	}
	return docs
}

func memoryDocOverviewFromFile(name, raw string, mod time.Time) MemoryDocOverview {
	body := raw
	head := ""
	if strings.HasPrefix(body, "---\n") {
		rest := body[4:]
		if i := strings.Index(rest, "\n---\n"); i >= 0 {
			head = rest[:i]
			body = rest[i+5:]
		}
	}
	meta := parseMemoryFrontmatter(head)
	title := strings.TrimSpace(meta["title"])
	if title == "" {
		title = firstMemoryHeading(body)
	}
	if title == "" {
		title = strings.TrimSuffix(name, filepath.Ext(name))
	}
	summary := strings.TrimSpace(meta["summary"])
	if summary == "" {
		summary = summarizeMemoryText(body, 160)
	}
	updatedAt := mod
	if parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(meta["updated_at"])); err == nil {
		updatedAt = parsed
	}
	return MemoryDocOverview{
		ID:        strings.TrimSuffix(name, filepath.Ext(name)),
		Title:     title,
		Summary:   summary,
		Kind:      strings.TrimSpace(meta["kind"]),
		UpdatedAt: updatedAt,
	}
}

func parseMemoryFrontmatter(head string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(head, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.Trim(strings.TrimSpace(value), `"`)
		if key != "" {
			out[key] = value
		}
	}
	return out
}

func firstMemoryHeading(body string) string {
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "# ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "# "))
		}
	}
	return ""
}

func summarizeMemoryText(s string, n int) string {
	text := strings.Join(strings.Fields(strings.TrimSpace(s)), " ")
	if len([]rune(text)) <= n {
		return text
	}
	runes := []rune(text)
	return strings.TrimSpace(string(runes[:n-1])) + "…"
}

func sanitizeMemoryPath(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "_"
	}
	r := strings.NewReplacer("/", "_", "\\", "_", ":", "_", "\n", "_", "\t", "_")
	return r.Replace(s)
}

func memoryScopeLabel(kind, key string) string {
	if strings.TrimSpace(kind) == "workspace" {
		return "workspace"
	}
	if strings.TrimSpace(key) == "" {
		return strings.TrimSpace(kind)
	}
	return strings.TrimSpace(kind) + ":" + strings.TrimSpace(key)
}

func memoryScopePublicKey(kind, key string) string {
	if strings.TrimSpace(kind) == "workspace" {
		return ""
	}
	return strings.TrimSpace(key)
}

func firstMemoryScope(ctx context.Context) (string, string) {
	if scopes := command.MemoryScopesFrom(ctx); len(scopes) > 0 {
		return strings.TrimSpace(scopes[0].Kind), strings.TrimSpace(scopes[0].Key)
	}
	if p := strings.TrimSpace(command.PersonaFrom(ctx)); p != "" {
		return "persona", p
	}
	return "global", ""
}

func explicitRememberRequest(s string) bool {
	lower := strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(s)), " "))
	for _, phrase := range []string{
		"记住",
		"记得",
		"以后都",
		"以后请",
		"以后要",
		"长期偏好",
		"作为长期",
		"you should remember",
		"please remember",
		"remember that",
		"remember i",
		"from now on",
	} {
		if strings.Contains(lower, phrase) {
			return true
		}
	}
	return false
}

func rememberTitle(input string) string {
	body := rememberBody(input)
	runes := []rune(body)
	if len(runes) > 42 {
		body = string(runes[:42]) + "..."
	}
	if strings.TrimSpace(body) == "" {
		return "User memory"
	}
	return body
}

func rememberBody(input string) string {
	body := strings.TrimSpace(input)
	prefixes := []string{
		"请记住", "帮我记住", "你记住", "记住", "记得",
		"please remember that", "please remember", "remember that",
	}
	lower := strings.ToLower(body)
	for _, p := range prefixes {
		lp := strings.ToLower(p)
		if strings.HasPrefix(lower, lp) {
			body = strings.TrimSpace(body[len(p):])
			break
		}
	}
	body = strings.TrimLeft(body, "：:，,。. ")
	if body == "" {
		body = strings.TrimSpace(input)
	}
	return fmt.Sprintf("User explicitly asked Sumi to remember: %s", body)
}
