package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/abcdlsj/sumi/command"
	"github.com/abcdlsj/sumi/tool"
)

type readTool struct{ s *store }

func (t *readTool) Name() string { return "read_memory" }
func (t *readTool) Desc() string { return "Read recent memory docs from a scope" }
func (t *readTool) Schema() map[string]any {
	return tool.ObjectSchema(
		tool.Prop("scope_kind", "string", "Scope kind"),
		tool.Prop("scope_key", "string", "Scope key"),
		tool.Prop("limit", "integer", "Maximum results"),
	)
}

func (t *readTool) Run(ctx context.Context, args json.RawMessage) (string, error) {
	var in readArgs
	if err := decode("read_memory", args, &in); err != nil {
		return "", err
	}
	limit := defaultLimit(in.Limit, 3)
	sc := t.s.resolveReadScope(ctx, command.SourceFrom(ctx), in.ScopeKind, in.ScopeKey)
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
	return tool.ObjectSchema(
		tool.Prop("query", "string", "Search query"),
		tool.Prop("scope_kind", "string", "Scope kind"),
		tool.Prop("scope_key", "string", "Scope key"),
		tool.Prop("limit", "integer", "Maximum results"),
		tool.Required("query"),
	)
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
	scopes := t.s.resolveSearchScopes(ctx, command.SourceFrom(ctx), in.ScopeKind, in.ScopeKey)
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
	return tool.ObjectSchema(
		tool.Prop("scope_kind", "string", "Scope kind"),
		tool.Prop("scope_key", "string", "Scope key"),
		tool.Prop("title", "string", "Title"),
		tool.Prop("body", "string", "Body"),
		tool.Prop("summary", "string", "Summary"),
		tool.Prop("kind", "string", "Memory kind"),
		tool.StringArrayProp("tags", "Tags"),
		tool.Required("title", "body"),
	)
}

func (t *writeTool) Run(ctx context.Context, args json.RawMessage) (string, error) {
	var in writeArgs
	if err := decode("write_memory", args, &in); err != nil {
		return "", err
	}
	if strings.TrimSpace(in.Title) == "" || strings.TrimSpace(in.Body) == "" {
		return "", fmt.Errorf("title and body are required")
	}
	sc := t.s.resolveWriteScope(ctx, command.SourceFrom(ctx), in.ScopeKind, in.ScopeKey)
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
