package builtin

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/abcdlsj/mink/bus"
	"github.com/abcdlsj/mink/command"
	"github.com/abcdlsj/mink/memory"
	rtsqlite "github.com/abcdlsj/mink/runtime/store/sqlite"
)

type memoryCmd struct {
	mem *memory.Store
	rt  *rtsqlite.DB
}

func NewMemoryCmd(mem *memory.Store, rt *rtsqlite.DB) command.Command {
	return &memoryCmd{mem: mem, rt: rt}
}

func (c *memoryCmd) Name() string { return "memory" }
func (c *memoryCmd) Desc() string { return "memory lookup and notes (!memory recent|search|save)" }

func (c *memoryCmd) Run(ctx context.Context, args []string) (string, error) {
	if c.mem == nil {
		return "memory store unavailable", nil
	}
	if len(args) == 0 {
		return "usage: !memory [recent [scope] [limit] | search [scope] <query> | save [scope] <title> :: <body>]", nil
	}

	switch args[0] {
	case "recent", "read":
		scope, rest, err := c.consumeScope(ctx, args[1:], false)
		if err != nil {
			return "", err
		}
		limit := 5
		if len(rest) > 0 {
			n, err := strconv.Atoi(strings.TrimSpace(rest[0]))
			if err != nil || n <= 0 {
				return "usage: !memory recent [scope] [limit]", nil
			}
			if n > 20 {
				n = 20
			}
			limit = n
		}
		docs, err := c.mem.RecentByScope(ctx, scope, limit)
		if err != nil {
			return "", err
		}
		return renderMemoryDocs("recent", []memory.Scope{scope}, docs), nil
	case "search":
		scopes, rest, err := c.consumeSearchScopes(ctx, args[1:])
		if err != nil {
			return "", err
		}
		query := strings.TrimSpace(strings.Join(rest, " "))
		if query == "" {
			return "usage: !memory search [scope] <query>", nil
		}
		docs, err := c.mem.SearchScoped(ctx, scopes, query, 8)
		if err != nil {
			return "", err
		}
		return renderMemoryDocs("search", scopes, docs), nil
	case "save", "write":
		scope, rest, err := c.consumeScope(ctx, args[1:], false)
		if err != nil {
			return "", err
		}
		raw := strings.TrimSpace(strings.Join(rest, " "))
		title, body, ok := strings.Cut(raw, "::")
		if !ok {
			return "usage: !memory save [scope] <title> :: <body>", nil
		}
		title = strings.TrimSpace(title)
		body = strings.TrimSpace(body)
		if title == "" || body == "" {
			return "usage: !memory save [scope] <title> :: <body>", nil
		}
		doc, err := c.mem.PutScoped(ctx, scope, memory.Doc{
			Title:   title,
			Body:    body,
			Summary: summarize(body, 140),
			Source:  strings.TrimSpace(bus.SourceFrom(ctx)),
		})
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("saved memory %s in %s", doc.ID, doc.Scope.String()), nil
	default:
		return "usage: !memory [recent [scope] [limit] | search [scope] <query> | save [scope] <title> :: <body>]", nil
	}
}

func (c *memoryCmd) consumeScope(ctx context.Context, args []string, allowEmpty bool) (memory.Scope, []string, error) {
	if len(args) > 0 && isScopeToken(args[0]) {
		scope, err := c.resolveScope(ctx, args[0])
		return scope, args[1:], err
	}
	if allowEmpty {
		return memory.Scope{}, args, nil
	}
	return c.defaultScope(ctx), args, nil
}

func (c *memoryCmd) consumeSearchScopes(ctx context.Context, args []string) ([]memory.Scope, []string, error) {
	if len(args) > 0 && isScopeToken(args[0]) {
		scope, err := c.resolveScope(ctx, args[0])
		if err != nil {
			return nil, nil, err
		}
		return []memory.Scope{scope}, args[1:], nil
	}
	scopes := []memory.Scope{c.defaultScope(ctx)}
	if src := strings.TrimSpace(bus.SourceFrom(ctx)); src != "" {
		scopes = append(scopes, memory.ChannelScope(src))
	}
	scopes = append(scopes, memory.GlobalScope())
	return dedupeScopes(scopes), args, nil
}

func (c *memoryCmd) defaultScope(ctx context.Context) memory.Scope {
	if c.rt != nil {
		return memory.WorkspaceScope(c.rt.WorkspaceID())
	}
	if src := strings.TrimSpace(bus.SourceFrom(ctx)); src != "" {
		return memory.ChannelScope(src)
	}
	return memory.GlobalScope()
}

func (c *memoryCmd) resolveScope(ctx context.Context, raw string) (memory.Scope, error) {
	raw = strings.TrimSpace(raw)
	switch raw {
	case "", "workspace":
		if c.rt == nil {
			return memory.Scope{}, fmt.Errorf("workspace scope unavailable")
		}
		return memory.WorkspaceScope(c.rt.WorkspaceID()), nil
	case "global":
		return memory.GlobalScope(), nil
	case "channel":
		src := strings.TrimSpace(bus.SourceFrom(ctx))
		if src == "" {
			return memory.Scope{}, fmt.Errorf("channel scope unavailable")
		}
		return memory.ChannelScope(src), nil
	default:
		scope := memory.ParseScope(raw)
		if scope.Kind == memory.ScopeTeam || scope.Kind == memory.ScopeAgent || scope.Kind == memory.ScopeChannel || scope.Kind == memory.ScopeWorkspace {
			if scope.Key == "" && scope.Kind != memory.ScopeGlobal {
				return memory.Scope{}, fmt.Errorf("scope key required for %s", scope.Kind)
			}
		}
		return scope, nil
	}
}

func renderMemoryDocs(mode string, scopes []memory.Scope, docs []memory.Doc) string {
	var b strings.Builder
	b.WriteString("Memory " + mode)
	if len(scopes) > 0 {
		var parts []string
		for _, scope := range scopes {
			parts = append(parts, scope.String())
		}
		b.WriteString(" [")
		b.WriteString(strings.Join(parts, ", "))
		b.WriteString("]")
	}
	b.WriteByte('\n')
	if len(docs) == 0 {
		b.WriteString("no memory docs")
		return b.String()
	}
	for _, doc := range docs {
		line := strings.TrimSpace(doc.Summary)
		if line == "" {
			line = summarize(doc.Body, 160)
		}
		fmt.Fprintf(&b, "- %s (%s): %s\n", doc.Title, doc.Scope.String(), line)
	}
	return strings.TrimSpace(b.String())
}

func summarize(s string, n int) string {
	s = strings.Join(strings.Fields(strings.TrimSpace(s)), " ")
	if len([]rune(s)) <= n {
		return s
	}
	r := []rune(s)
	if n <= 1 {
		return "…"
	}
	return string(r[:n-1]) + "…"
}

func isScopeToken(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	switch s {
	case "global", "workspace", "channel":
		return true
	}
	return strings.Contains(s, ":")
}

func dedupeScopes(scopes []memory.Scope) []memory.Scope {
	seen := map[string]bool{}
	out := make([]memory.Scope, 0, len(scopes))
	for _, scope := range scopes {
		scope = memory.ParseScope(scope.String())
		if seen[scope.String()] {
			continue
		}
		seen[scope.String()] = true
		out = append(out, scope)
	}
	return out
}
