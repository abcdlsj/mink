package sessioncmd

import (
	"context"
	"strings"

	"github.com/abcdlsj/mink/app"
	"github.com/abcdlsj/mink/command"
	"github.com/abcdlsj/mink/msg"
	"github.com/abcdlsj/mink/session"
)

func estimate(m msg.Message) int {
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

func cur(ctx context.Context, a *app.App) (*session.Session, error) {
	return a.CurrentSession(command.SourceFrom(ctx))
}

func sessionLine(s *session.Session) string {
	line := s.ID
	if strings.TrimSpace(s.Title) != "" {
		line += " [" + s.Title + "]"
	}
	if strings.TrimSpace(s.Summary) != "" {
		line += " {" + trim(s.Summary, 48) + "}"
	}
	return line
}

func clone(in []msg.Message) []msg.Message {
	out := make([]msg.Message, 0, len(in))
	for _, m := range in {
		cp := m
		cp.ToolCalls = append([]msg.ToolCall(nil), m.ToolCalls...)
		cp.ToolResults = append([]msg.ToolResult(nil), m.ToolResults...)
		out = append(out, cp)
	}
	return out
}

func trim(s string, n int) string {
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

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		s = strings.TrimSpace(s)
		if s != "" {
			return s
		}
	}
	return ""
}

func blank(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return strings.TrimSpace(s)
}
