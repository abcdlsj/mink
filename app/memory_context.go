package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/abcdlsj/sumi/agent"
	"github.com/abcdlsj/sumi/command"
)

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
