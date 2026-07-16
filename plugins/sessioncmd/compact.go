package sessioncmd

import (
	"context"
	"strings"
	"time"

	"github.com/abcdlsj/sumi/app"
	"github.com/abcdlsj/sumi/command"
	"github.com/abcdlsj/sumi/msg"
	"github.com/abcdlsj/sumi/session"
)

type compactCmd struct {
	app *app.App
}

func (c *compactCmd) Name() string { return "compact" }

func (c *compactCmd) Desc() string {
	return "compact current conversation context"
}

func (c *compactCmd) Run(ctx context.Context, args []string) (string, error) {
	if c.app.ManualCompactSpaceBacked(command.SourceFrom(ctx)) {
		return "", app.ErrManualCompactSpaceBacked
	}
	s, err := cur(ctx, c.app)
	if err != nil {
		return "", err
	}
	note := strings.TrimSpace(strings.Join(args, " "))
	s.Compact(composeSummary(s, note), 8)
	if err := c.app.SaveSession(s); err != nil {
		return "", err
	}
	if note == "" {
		return "compact requested", nil
	}
	return "compact requested with note", nil
}

func composeSummary(s *session.Session, note string) string {
	var b strings.Builder
	if strings.TrimSpace(note) != "" {
		b.WriteString("Note: " + strings.TrimSpace(note) + "\n")
	}
	start := 0
	if len(s.Messages) > 12 {
		start = len(s.Messages) - 12
	}
	for _, m := range s.Messages[start:] {
		text := trim(primaryText(m), 160)
		if text == "" {
			continue
		}
		b.WriteString(m.Role + ": " + text + "\n")
	}
	out := strings.TrimSpace(b.String())
	if out == "" {
		out = "Conversation compacted at " + time.Now().Format(time.RFC3339)
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
