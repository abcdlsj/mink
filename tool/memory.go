package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/abcdlsj/mink/bus"
	"github.com/abcdlsj/mink/internal/xstr"
	"github.com/abcdlsj/mink/memory"
	rtsqlite "github.com/abcdlsj/mink/runtime/store/sqlite"
)

type scopeResolver struct {
	rt      *rtsqlite.DB
	agentID string
}

func (r scopeResolver) explicit(kind, key string) (memory.Scope, error) {
	kind = strings.TrimSpace(kind)
	key = strings.TrimSpace(key)
	if kind == "" && key == "" {
		return memory.Scope{}, nil
	}
	if kind == "" {
		return memory.Scope{}, fmt.Errorf("scope_kind is required when scope_key is set")
	}
	return memory.CustomScope(kind, key), nil
}

func (r scopeResolver) identity(ctx context.Context) (memory.Scope, bool) {
	if r.rt == nil || strings.TrimSpace(r.agentID) == "" {
		return memory.Scope{}, false
	}
	identity, err := r.rt.GetAgentIdentity(ctx, r.agentID)
	if err != nil {
		return memory.Scope{}, false
	}
	scope := strings.TrimSpace(identity.MemoryScope)
	if scope == "" {
		return memory.Scope{}, false
	}
	return memory.ParseScope(scope), true
}

func (r scopeResolver) read(ctx context.Context, kind, key string) (memory.Scope, error) {
	scope, err := r.explicit(kind, key)
	if err != nil {
		return memory.Scope{}, err
	}
	if scope.Kind != "" {
		return scope, nil
	}
	if scope, ok := r.identity(ctx); ok {
		return scope, nil
	}
	if src := strings.TrimSpace(bus.SourceFrom(ctx)); src != "" {
		return memory.ChannelScope(src), nil
	}
	if r.rt != nil {
		return memory.WorkspaceScope(r.rt.WorkspaceID()), nil
	}
	if strings.TrimSpace(r.agentID) != "" {
		return memory.AgentScope(r.agentID), nil
	}
	return memory.GlobalScope(), nil
}

func (r scopeResolver) write(ctx context.Context, kind, key string) (memory.Scope, error) {
	scope, err := r.explicit(kind, key)
	if err != nil {
		return memory.Scope{}, err
	}
	if scope.Kind != "" {
		return scope, nil
	}
	if scope, ok := r.identity(ctx); ok {
		return scope, nil
	}
	if strings.TrimSpace(r.agentID) != "" {
		return memory.AgentScope(r.agentID), nil
	}
	if src := strings.TrimSpace(bus.SourceFrom(ctx)); src != "" {
		return memory.ChannelScope(src), nil
	}
	if r.rt != nil {
		return memory.WorkspaceScope(r.rt.WorkspaceID()), nil
	}
	return memory.GlobalScope(), nil
}

func (r scopeResolver) search(ctx context.Context, kind, key string) ([]memory.Scope, error) {
	scope, err := r.explicit(kind, key)
	if err != nil {
		return nil, err
	}
	if scope.Kind != "" {
		return []memory.Scope{scope}, nil
	}

	var out []memory.Scope
	seen := map[string]bool{}
	add := func(scope memory.Scope) {
		scope = memory.ParseScope(scope.String())
		if scope.Kind == "" {
			return
		}
		if seen[scope.String()] {
			return
		}
		seen[scope.String()] = true
		out = append(out, scope)
	}

	if src := strings.TrimSpace(bus.SourceFrom(ctx)); src != "" {
		add(memory.ChannelScope(src))
	}
	if scope, ok := r.identity(ctx); ok {
		add(scope)
	}
	if strings.TrimSpace(r.agentID) != "" {
		add(memory.AgentScope(r.agentID))
	}
	if r.rt != nil {
		add(memory.WorkspaceScope(r.rt.WorkspaceID()))
	}
	add(memory.GlobalScope())
	return out, nil
}

type SearchMemory struct {
	mem *memory.Store
	res scopeResolver
}

func NewSearchMemory(mem *memory.Store, rt *rtsqlite.DB, agentID string) *SearchMemory {
	return &SearchMemory{mem: mem, res: scopeResolver{rt: rt, agentID: agentID}}
}

func (s *SearchMemory) Name() string { return "search_memory" }
func (s *SearchMemory) Desc() string {
	return "Search memory across scoped notes. Defaults to current channel, agent, workspace, then global."
}
func (s *SearchMemory) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query":      map[string]string{"type": "string", "description": "Search query"},
			"scope_kind": map[string]string{"type": "string", "description": "Optional scope kind: global, workspace, team, agent, channel, or custom"},
			"scope_key":  map[string]string{"type": "string", "description": "Optional scope key"},
			"limit":      map[string]any{"type": "integer", "description": "Max results, default 5"},
		},
		"required": []string{"query"},
	}
}
func (s *SearchMemory) Run(ctx context.Context, args json.RawMessage) (string, error) {
	var params struct {
		Query     string `json:"query"`
		ScopeKind string `json:"scope_kind"`
		ScopeKey  string `json:"scope_key"`
		Limit     int    `json:"limit"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", ParseError(s.Name(), err.Error(), string(args))
	}
	scopes, err := s.res.search(ctx, params.ScopeKind, params.ScopeKey)
	if err != nil {
		return "", ParseError(s.Name(), err.Error(), string(args))
	}
	limit := params.Limit
	if limit <= 0 {
		limit = 5
	}
	docs, err := s.mem.SearchScoped(ctx, scopes, params.Query, limit)
	if err != nil {
		return "", WrapError(s.Name(), err)
	}
	return renderDocs("search_memory", scopes, docs, 400), nil
}

type ReadMemory struct {
	mem *memory.Store
	res scopeResolver
}

func NewReadMemory(mem *memory.Store, rt *rtsqlite.DB, agentID string) *ReadMemory {
	return &ReadMemory{mem: mem, res: scopeResolver{rt: rt, agentID: agentID}}
}

func (r *ReadMemory) Name() string { return "read_memory" }
func (r *ReadMemory) Desc() string {
	return "Read recent memory docs from a scope. Defaults to the agent's configured scope, then channel, workspace, or agent."
}
func (r *ReadMemory) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"scope_kind": map[string]string{"type": "string", "description": "Optional scope kind: global, workspace, team, agent, channel, or custom"},
			"scope_key":  map[string]string{"type": "string", "description": "Optional scope key"},
			"limit":      map[string]any{"type": "integer", "description": "Max docs, default 3"},
		},
	}
}
func (r *ReadMemory) Run(ctx context.Context, args json.RawMessage) (string, error) {
	var params struct {
		ScopeKind string `json:"scope_kind"`
		ScopeKey  string `json:"scope_key"`
		Limit     int    `json:"limit"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", ParseError(r.Name(), err.Error(), string(args))
	}
	scope, err := r.res.read(ctx, params.ScopeKind, params.ScopeKey)
	if err != nil {
		return "", ParseError(r.Name(), err.Error(), string(args))
	}
	limit := params.Limit
	if limit <= 0 {
		limit = 3
	}
	docs, err := r.mem.RecentByScope(ctx, scope, limit)
	if err != nil {
		return "", WrapError(r.Name(), err)
	}
	return renderDocs("read_memory", []memory.Scope{scope}, docs, 1200), nil
}

type WriteMemory struct {
	mem *memory.Store
	res scopeResolver
}

func NewWriteMemory(mem *memory.Store, rt *rtsqlite.DB, agentID string) *WriteMemory {
	return &WriteMemory{mem: mem, res: scopeResolver{rt: rt, agentID: agentID}}
}

func (w *WriteMemory) Name() string { return "write_memory" }
func (w *WriteMemory) Desc() string {
	return "Write a memory doc. Defaults to the agent's configured scope, otherwise the current agent private scope."
}
func (w *WriteMemory) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"scope_kind": map[string]string{"type": "string", "description": "Optional scope kind: global, workspace, team, agent, channel, or custom"},
			"scope_key":  map[string]string{"type": "string", "description": "Optional scope key"},
			"title":      map[string]string{"type": "string", "description": "Doc title"},
			"body":       map[string]string{"type": "string", "description": "Doc body"},
			"summary":    map[string]string{"type": "string", "description": "Optional short summary"},
			"kind":       map[string]string{"type": "string", "description": "Optional doc kind, default note"},
			"tags": map[string]any{
				"type":        "array",
				"description": "Optional tags",
				"items":       map[string]string{"type": "string"},
			},
		},
		"required": []string{"title", "body"},
	}
}
func (w *WriteMemory) Run(ctx context.Context, args json.RawMessage) (string, error) {
	var params struct {
		ScopeKind string   `json:"scope_kind"`
		ScopeKey  string   `json:"scope_key"`
		Title     string   `json:"title"`
		Body      string   `json:"body"`
		Summary   string   `json:"summary"`
		Kind      string   `json:"kind"`
		Tags      []string `json:"tags"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", ParseError(w.Name(), err.Error(), string(args))
	}
	scope, err := w.res.write(ctx, params.ScopeKind, params.ScopeKey)
	if err != nil {
		return "", ParseError(w.Name(), err.Error(), string(args))
	}
	doc, err := w.mem.PutScoped(ctx, scope, memory.Doc{
		Title:   strings.TrimSpace(params.Title),
		Body:    strings.TrimSpace(params.Body),
		Summary: strings.TrimSpace(params.Summary),
		Kind:    strings.TrimSpace(params.Kind),
		Tags:    trimTags(params.Tags),
		Source:  strings.TrimSpace(bus.SourceFrom(ctx)),
	})
	if err != nil {
		return "", WrapError(w.Name(), err)
	}
	return fmt.Sprintf("wrote memory id=%s scope=%s title=%q", doc.ID, doc.Scope.String(), doc.Title), nil
}

func trimTags(tags []string) []string {
	out := tags[:0]
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag != "" {
			out = append(out, tag)
		}
	}
	return out
}

func renderDocs(name string, scopes []memory.Scope, docs []memory.Doc, bodyLimit int) string {
	var b strings.Builder
	if len(scopes) > 0 {
		var scopeNames []string
		for _, scope := range scopes {
			scopeNames = append(scopeNames, scope.String())
		}
		fmt.Fprintf(&b, "%s scopes: %s\n", name, strings.Join(scopeNames, ", "))
	}
	if len(docs) == 0 {
		b.WriteString("no memory docs found")
		return b.String()
	}
	for i, doc := range docs {
		fmt.Fprintf(&b, "\n[%d] %s\n", i+1, strings.TrimSpace(doc.Title))
		fmt.Fprintf(&b, "id: %s\n", doc.ID)
		fmt.Fprintf(&b, "scope: %s\n", doc.Scope.String())
		if kind := strings.TrimSpace(doc.Kind); kind != "" {
			fmt.Fprintf(&b, "kind: %s\n", kind)
		}
		if len(doc.Tags) > 0 {
			fmt.Fprintf(&b, "tags: %s\n", strings.Join(doc.Tags, ", "))
		}
		if !doc.UpdatedAt.IsZero() {
			fmt.Fprintf(&b, "updated_at: %s\n", doc.UpdatedAt.UTC().Format(time.RFC3339))
		}
		if summary := strings.TrimSpace(doc.Summary); summary != "" {
			fmt.Fprintf(&b, "summary: %s\n", xstr.TruncateASCII(summary, 240))
		}
		if body := strings.TrimSpace(doc.Body); body != "" {
			fmt.Fprintf(&b, "body:\n%s\n", xstr.TruncateASCII(body, bodyLimit))
		}
	}
	return strings.TrimSpace(b.String())
}
