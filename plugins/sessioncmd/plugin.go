package sessioncmd

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/abcdlsj/mink/app"
	"github.com/abcdlsj/mink/bus"
	"github.com/abcdlsj/mink/command"
	"github.com/abcdlsj/mink/msg"
	"github.com/abcdlsj/mink/session"
)

func Plugin() app.Plugin {
	return func(a *app.App) error {
		a.RegisterCommand(&sessionCmd{app: a})
		a.RegisterCommand(&compactCmd{app: a})
		a.RegisterCommand(&tokensCmd{app: a})
		a.RegisterCommand(&replayCmd{app: a})
		return nil
	}
}

const sessionUsage = "usage: !session [list|current|new|switch <id>|fork|close]"
const replayUsage = "usage: !replay [count]"

type sessionCmd struct {
	app *app.App
}

func (c *sessionCmd) Name() string { return "session" }

func (c *sessionCmd) Desc() string {
	return "session management (list/current/new/switch/fork/close)"
}

func (c *sessionCmd) Run(ctx context.Context, args []string) (string, error) {
	if len(args) == 0 {
		return sessionUsage, nil
	}
	switch args[0] {
	case "list":
		return c.list(ctx)
	case "current":
		return c.current(ctx)
	case "new":
		return c.new(ctx)
	case "switch":
		return c.switchTo(ctx, args[1:])
	case "fork":
		return c.fork(ctx)
	case "close":
		return c.close(ctx)
	default:
		return sessionUsage, nil
	}
}

func (c *sessionCmd) list(ctx context.Context) (string, error) {
	ss, err := c.app.ListSessions()
	if err != nil {
		return "", err
	}
	if len(ss) == 0 {
		return "no sessions", nil
	}
	now, _ := cur(ctx, c.app)
	var b strings.Builder
	b.WriteString("Sessions:\n")
	for _, s := range ss {
		mark := "  "
		if now != nil && now.ID == s.ID {
			mark = "* "
		}
		fmt.Fprintf(&b, "%s%s\n", mark, sessionLine(s))
	}
	return strings.TrimSpace(b.String()), nil
}

func (c *sessionCmd) current(ctx context.Context) (string, error) {
	s, err := cur(ctx, c.app)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Current session: %s [%s]\nEntries: %d", s.ID, blank(s.Title, "(untitled)"), len(s.Messages)), nil
}

func (c *sessionCmd) new(ctx context.Context) (string, error) {
	s, err := c.app.NewSession(command.SourceFrom(ctx))
	if err != nil {
		return "", err
	}
	return "new session: " + s.ID, nil
}

func (c *sessionCmd) switchTo(ctx context.Context, args []string) (string, error) {
	if len(args) == 0 {
		return "usage: !session switch <id>", nil
	}
	s, err := c.app.SwitchSession(command.SourceFrom(ctx), args[0])
	if err != nil {
		return "", err
	}
	return "switched session: " + s.ID, nil
}

func (c *sessionCmd) fork(ctx context.Context) (string, error) {
	s, err := cur(ctx, c.app)
	if err != nil {
		return "", err
	}
	next, err := c.app.NewSession(command.SourceFrom(ctx))
	if err != nil {
		return "", err
	}
	next.Title = s.Title
	next.Summary = s.Summary
	next.Messages = clone(s.Messages)
	if err := c.app.SaveSession(next); err != nil {
		return "", err
	}
	return "forked current session: " + next.ID, nil
}

func (c *sessionCmd) close(ctx context.Context) (string, error) {
	next, err := c.app.NewSession(command.SourceFrom(ctx))
	if err != nil {
		return "", err
	}
	return "closed current session\nswitched to new session: " + next.ID, nil
}

type compactCmd struct {
	app *app.App
}

func (c *compactCmd) Name() string { return "compact" }

func (c *compactCmd) Desc() string {
	return "compact current conversation context"
}

func (c *compactCmd) Run(ctx context.Context, args []string) (string, error) {
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

type tokensCmd struct {
	app *app.App
}

func (c *tokensCmd) Name() string { return "tokens" }

func (c *tokensCmd) Desc() string {
	return "show estimated token usage for current session"
}

func (c *tokensCmd) Run(ctx context.Context, args []string) (string, error) {
	s, err := cur(ctx, c.app)
	if err != nil {
		return "", err
	}
	var total, user, assistant, system, tooln int
	for _, m := range s.Messages {
		n := estimate(m)
		total += n
		switch m.Role {
		case "user":
			user += n
		case "assistant":
			assistant += n
		case "system":
			system += n
		case "tool":
			tooln += n
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Estimated tokens\n")
	fmt.Fprintf(&b, "  total: %d\n", total)
	fmt.Fprintf(&b, "  messages: %d\n", len(s.Messages))
	fmt.Fprintf(&b, "  input(user): %d\n", user)
	fmt.Fprintf(&b, "  output(assistant): %d\n", assistant)
	fmt.Fprintf(&b, "  system: %d\n", system)
	fmt.Fprintf(&b, "  tool: %d\n", tooln)
	return strings.TrimRight(b.String(), "\n"), nil
}

type replayCmd struct {
	app *app.App
}

func (c *replayCmd) Name() string { return "replay" }

func (c *replayCmd) Desc() string {
	return "replay current session activity (!replay [count])"
}

func (c *replayCmd) Run(ctx context.Context, args []string) (string, error) {
	s, err := cur(ctx, c.app)
	if err != nil {
		return "", err
	}
	n := 30
	if len(args) > 0 {
		v, err := strconv.Atoi(strings.TrimSpace(args[0]))
		if err != nil || v <= 0 {
			return replayUsage, nil
		}
		if v > 200 {
			v = 200
		}
		n = v
	}
	evs, err := c.app.ReplaySession(s.ID, n)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Replay session %s (last %d events)\n", s.ID, len(evs))
	for _, ev := range evs {
		fmt.Fprintf(&b, "%s [%s] %s\n", ev.Time.Format("15:04:05"), ev.Type, replayLine(ev))
	}
	return strings.TrimSpace(b.String()), nil
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

func replayLine(ev bus.Event) string {
	parts := []string{}
	if ev.Tool != "" {
		parts = append(parts, ev.Tool)
	}
	if text := firstNonEmpty(ev.Text, ev.Output, ev.Input, ev.Err); text != "" {
		parts = append(parts, trim(text, 96))
	}
	if len(parts) == 0 {
		return "(empty)"
	}
	return strings.Join(parts, " ")
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
