package app

import (
	"context"
	"strings"
	"time"

	"github.com/abcdlsj/sumi/bus"
	"github.com/abcdlsj/sumi/msg"
	"github.com/abcdlsj/sumi/session"
	"github.com/abcdlsj/sumi/textutil"
)

func (a *App) compactSession(ctx context.Context, s *session.Session) (string, error) {
	return a.compactSessionKeep(ctx, s, 8)
}

func (a *App) compactSessionKeep(ctx context.Context, s *session.Session, keep int) (string, error) {
	if len(s.Messages) == 0 {
		return "empty session", nil
	}
	summary, err := a.buildCompactSummary(ctx, s)
	if err != nil {
		return "", err
	}
	s.Compact(summary, keep)
	return s.Summary, nil
}

func (a *App) buildCompactSummary(ctx context.Context, s *session.Session) (string, error) {
	if len(s.Messages) == 0 {
		return "empty session", nil
	}
	var b strings.Builder
	for _, m := range s.Messages {
		switch m.Role {
		case "user", "assistant":
			b.WriteString(m.Role + ": " + m.Content + "\n")
		case "tool":
			for _, tr := range m.ToolResults {
				b.WriteString("tool: " + tr.Content + "\n")
			}
		}
	}
	if a.provider != nil {
		resp, err := a.provider.Chat(ctx, []msg.Message{
			{Role: "system", Content: "Summarize the conversation for future continuation. Keep it short and factual."},
			{Role: "user", Content: b.String()},
		}, nil)
		if err == nil {
			return strings.TrimSpace(resp.Content), nil
		}
	}
	return heuristicSummary(s.Messages), nil
}

func (a *App) autoCompact(ctx context.Context, source, runtime string, s *session.Session) error {
	if !a.shouldAutoCompact(runtime, s) {
		return nil
	}
	summary, err := a.compactSessionKeep(ctx, s, a.cfg.Compact.KeepRecentMessages)
	if err != nil {
		return err
	}
	if err := a.sessions.Save(s); err != nil {
		return err
	}
	a.bus.Publish(bus.Event{
		Type:      bus.SessionCompacted,
		Source:    source,
		SessionID: s.ID,
		Text:      summary,
	})
	return nil
}

func (a *App) shouldAutoCompact(runtime string, s *session.Session) bool {
	if !a.cfg.Compact.Auto || s == nil || len(s.Messages) == 0 {
		return false
	}
	if isExternalDriverRuntime(runtime) {
		return false
	}
	if n := a.cfg.Compact.TriggerMessages; n > 0 && len(s.Messages) >= n {
		return true
	}
	if n := a.compactTokenLimit(); n > 0 && estimateMessages(s.Messages) >= n {
		return true
	}
	return false
}

func (a *App) compactTokenLimit() int {
	mc := a.cfg.Active
	if mc.ContextWindow > 0 {
		limit := mc.ContextWindow - max(mc.MaxTokens, a.cfg.MaxTokens) - a.cfg.Compact.ReserveTokens
		if limit > 0 {
			return limit
		}
	}
	if a.cfg.Compact.TriggerTokens > 0 {
		return a.cfg.Compact.TriggerTokens
	}
	return 0
}

func isExternalDriverRuntime(runtime string) bool {
	switch strings.TrimSpace(runtime) {
	case "claude", "codex":
		return true
	default:
		return false
	}
}

func heuristicSummary(msgs []msg.Message) string {
	var b strings.Builder
	start := 0
	if len(msgs) > 12 {
		start = len(msgs) - 12
	}
	for _, m := range msgs[start:] {
		text := primaryText(m)
		if text == "" {
			continue
		}
		b.WriteString(m.Role)
		b.WriteString(": ")
		b.WriteString(trimText(text, 160))
		b.WriteByte('\n')
	}
	out := strings.TrimSpace(b.String())
	if out == "" {
		return "Conversation compacted at " + time.Now().Format(time.RFC3339)
	}
	return out
}

func primaryText(m msg.Message) string {
	switch {
	case strings.TrimSpace(m.Content) != "":
		return m.Content
	case strings.TrimSpace(m.Reasoning) != "":
		return m.Reasoning
	case len(m.ToolCalls) > 0:
		var parts []string
		for _, tc := range m.ToolCalls {
			parts = append(parts, tc.Name+"("+strings.TrimSpace(string(tc.Args))+")")
		}
		return strings.Join(parts, "; ")
	case len(m.ToolResults) > 0:
		var parts []string
		for _, tr := range m.ToolResults {
			part := tr.Content
			if strings.TrimSpace(tr.Error) != "" {
				part = "error: " + tr.Error
			}
			parts = append(parts, part)
		}
		return strings.Join(parts, "; ")
	default:
		return ""
	}
}

func estimateMessages(msgs []msg.Message) int {
	total := 0
	for _, m := range msgs {
		total += estimateMessage(m)
	}
	return total
}

func estimateMessage(m msg.Message) int {
	n := len([]rune(m.Content)) + len([]rune(m.Reasoning))
	for _, tc := range m.ToolCalls {
		n += len([]rune(tc.Name)) + len([]rune(string(tc.Args)))
	}
	for _, tr := range m.ToolResults {
		n += len([]rune(tr.Content)) + len([]rune(tr.Error))
	}
	if n == 0 {
		return 0
	}
	return n/4 + 1
}

func trimText(s string, n int) string {
	return textutil.Ellipsis(strings.Join(strings.Fields(strings.TrimSpace(s)), " "), n)
}
