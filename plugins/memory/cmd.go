package memory

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/abcdlsj/sumi/command"
)

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
	case "proposals", "pending":
		return c.runProposals(ctx, args[1:])
	case "confirm":
		return c.runConfirm(ctx, args[1:])
	case "reject", "dismiss":
		return c.runReject(ctx, args[1:])
	case "delete", "remove":
		return c.runDelete(ctx, args[1:])
	default:
		return usageText, nil
	}
}

func (c *cmd) runRecent(ctx context.Context, args []string) (string, error) {
	src := command.SourceFrom(ctx)
	sc, rest := c.consumeScope(ctx, src, args)
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
	scopes, rest := c.consumeScopes(ctx, src, args)
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
	sc, rest := c.consumeScope(ctx, src, args)
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
	d, err := c.s.put(ctx, sc, memoryDocFromWrite(ctx, writeArgs{
		Title:   title,
		Body:    body,
		Summary: summarize(body, 140),
		Kind:    "note",
	}))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("saved memory %s in %s:%s", d.ID, d.ScopeKind, d.ScopeKey), nil
}

func (c *cmd) runProposals(ctx context.Context, args []string) (string, error) {
	items, err := c.s.listProposals()
	if err != nil {
		return "", err
	}
	return renderProposals(items), nil
}

func (c *cmd) runConfirm(ctx context.Context, args []string) (string, error) {
	if len(args) == 0 || strings.TrimSpace(args[0]) == "" {
		return confirmUsageText, nil
	}
	d, err := c.s.confirmProposal(ctx, args[0])
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("confirmed memory %s in %s:%s", d.ID, d.ScopeKind, d.ScopeKey), nil
}

func (c *cmd) runReject(ctx context.Context, args []string) (string, error) {
	if len(args) == 0 || strings.TrimSpace(args[0]) == "" {
		return rejectUsageText, nil
	}
	if err := c.s.rejectProposal(args[0]); err != nil {
		return "", err
	}
	return fmt.Sprintf("rejected memory proposal %s", strings.TrimSpace(args[0])), nil
}

func (c *cmd) runDelete(ctx context.Context, args []string) (string, error) {
	src := command.SourceFrom(ctx)
	sc, rest := c.consumeScope(ctx, src, args)
	if len(rest) == 0 || strings.TrimSpace(rest[0]) == "" {
		return deleteUsageText, nil
	}
	d, err := c.s.delete(ctx, sc, rest[0])
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("deleted memory %s from %s:%s", d.ID, d.ScopeKind, d.ScopeKey), nil
}

func (c *cmd) consumeScope(ctx context.Context, src string, args []string) (scope, []string) {
	if len(args) > 0 && isScopeToken(args[0]) {
		return parseScope(src, args[0], c.s.workspace), args[1:]
	}
	return c.s.resolveReadScope(ctx, src, "", ""), args
}

func (c *cmd) consumeScopes(ctx context.Context, src string, args []string) ([]scope, []string) {
	if len(args) > 0 && isScopeToken(args[0]) {
		return []scope{parseScope(src, args[0], c.s.workspace)}, args[1:]
	}
	return c.s.resolveSearchScopes(ctx, src, "", ""), args
}
