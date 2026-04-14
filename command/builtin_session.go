package command

import (
	"context"
	"fmt"
	"strings"

	"github.com/abcdlsj/mink/bus"
	"github.com/abcdlsj/mink/msg"
	"github.com/abcdlsj/mink/session"
)

type sessionCmd struct {
	sm    *session.Manager
	reset sessionResetter
}

func NewSessionCmd(sm *session.Manager, reset sessionResetter) Command {
	return &sessionCmd{sm: sm, reset: reset}
}

func (c *sessionCmd) Name() string { return "session" }
func (c *sessionCmd) Desc() string { return "session management (list/current/new/switch/fork/close)" }

func (c *sessionCmd) Run(ctx context.Context, args []string) (string, error) {
	src := bus.SourceFrom(ctx)
	if src == "" {
		src = bus.AddrPlatformCLI
	}
	if len(args) == 0 {
		return "usage: !session [list|current|new|switch <id>|fork|close]", nil
	}

	switch args[0] {
	case "list":
		ids, err := c.sm.List()
		if err != nil {
			return "", err
		}
		if len(ids) == 0 {
			return "no sessions", nil
		}
		var b strings.Builder
		b.WriteString("Sessions:\n")
		current, _ := c.sm.CurrentID(src)
		for _, id := range ids {
			mark := "  "
			if id == current {
				mark = "* "
			}
			line := id
			if s, err := c.sm.Get(id); err == nil && s != nil {
				meta := []string{}
				if status := strings.TrimSpace(s.Status()); status != "" {
					meta = append(meta, status)
				}
				if summary := strings.TrimSpace(s.Summary()); summary != "" {
					meta = append(meta, replayTrim(summary, 48))
				}
				if len(meta) > 0 {
					line += " [" + strings.Join(meta, " | ") + "]"
				}
			}
			fmt.Fprintf(&b, "%s%s\n", mark, line)
		}
		return b.String(), nil
	case "current":
		id, ok := c.sm.CurrentID(src)
		if !ok {
			return "no current session for source", nil
		}
		s, err := c.sm.Get(id)
		if err != nil {
			return "", err
		}
		var b strings.Builder
		fmt.Fprintf(&b, "Current session: %s\n", id)
		fmt.Fprintf(&b, "Kind: %s\n", s.Kind())
		fmt.Fprintf(&b, "Status: %s\n", s.Status())
		fmt.Fprintf(&b, "Source: %s\n", src)
		fmt.Fprintf(&b, "Entries: %d\n", s.EntryCount())
		fmt.Fprintf(&b, "Anchors: %d", len(s.Anchors()))
		if summary := strings.TrimSpace(s.Summary()); summary != "" {
			fmt.Fprintf(&b, "\nSummary: %s", summary)
		}
		if p := s.Provenance(); p != nil {
			fmt.Fprintf(&b, "\nParent: %s\nFork point: %d", p.ParentSessionID, p.ForkEntryCount)
		}
		return b.String(), nil
	case "new":
		s, err := c.sm.ResetSource(src)
		if err != nil {
			return "", err
		}
		s.SetKind("main")
		s.SetStatus("active")
		c.invalidate(src)
		return fmt.Sprintf("switched to new session: %s", s.ID()), nil
	case "switch":
		if len(args) < 2 {
			return "usage: !session switch <id>", nil
		}
		if err := c.sm.RestoreSource(src, args[1]); err != nil {
			return "", err
		}
		c.invalidate(src)
		return fmt.Sprintf("switched to session: %s", args[1]), nil
	case "fork":
		s, err := c.sm.ForkSource(src)
		if err != nil {
			return "", err
		}
		c.invalidate(src)
		return fmt.Sprintf("forked current session: %s", s.ID()), nil
	case "close":
		s, err := c.sm.Current(src)
		if err != nil {
			return "", err
		}
		s.SetStatus("closed")
		if err := s.Flush(); err != nil {
			return "", err
		}
		next, err := c.sm.ResetSource(src)
		if err != nil {
			return "", err
		}
		next.SetKind("main")
		next.SetStatus("active")
		c.invalidate(src)
		return fmt.Sprintf("closed session: %s\nswitched to new session: %s", s.ID(), next.ID()), nil
	default:
		return "usage: !session [list|current|new|switch <id>|fork|close]", nil
	}
}

func (c *sessionCmd) invalidate(src string) {
	if c.reset != nil {
		c.reset.InvalidateSource(src)
		if teamReset, ok := any(c.reset).(sessionTeamResetter); ok {
			teamReset.UnbindTeamSource(src)
		}
	}
}

type compactCmd struct {
	b *bus.Bus
}

func NewCompactCmd(b *bus.Bus) Command { return &compactCmd{b: b} }

func (c *compactCmd) Name() string { return "compact" }
func (c *compactCmd) Desc() string { return "compact current conversation context" }

func (c *compactCmd) Run(ctx context.Context, args []string) (string, error) {
	src := bus.SourceFrom(ctx)
	if src == "" {
		src = bus.AddrPlatformCLI
	}
	note := strings.TrimSpace(strings.Join(args, " "))

	if err := c.b.Pub(bus.Msg{
		Type:    bus.TypeSessionCompact,
		From:    src,
		To:      bus.AddrAgentMain,
		Payload: note,
	}); err != nil {
		return "", err
	}

	if note == "" {
		return "compact requested", nil
	}
	return "compact requested with note", nil
}

type tokensCmd struct {
	usage func(src string) (msg.TokenUsage, bool)
}

func NewTokensCmd(usage func(src string) (msg.TokenUsage, bool)) Command {
	return &tokensCmd{usage: usage}
}

func (c *tokensCmd) Name() string { return "tokens" }
func (c *tokensCmd) Desc() string { return "show estimated token usage for current session" }

func (c *tokensCmd) Run(ctx context.Context, args []string) (string, error) {
	src := bus.SourceFrom(ctx)
	if src == "" {
		src = bus.AddrPlatformCLI
	}

	u, ok := c.usage(src)
	if !ok {
		return "no active session for current source", nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Estimated tokens\n")
	fmt.Fprintf(&b, "  total: %d\n", u.Total)
	fmt.Fprintf(&b, "  messages: %d\n", u.Messages)
	fmt.Fprintf(&b, "  input(user): %d\n", u.Input)
	fmt.Fprintf(&b, "  output(assistant): %d\n", u.Output)
	fmt.Fprintf(&b, "  system: %d\n", u.System)
	fmt.Fprintf(&b, "  tool: %d\n", u.Tool)
	if u.CompactTrigger > 0 {
		fmt.Fprintf(&b, "  compact trigger: %d\n", u.CompactTrigger)
		if u.Total > 0 {
			fmt.Fprintf(&b, "  compact usage: %.1f%%\n", float64(u.Total)*100/float64(u.CompactTrigger))
		}
	}
	if u.ContextWindow > 0 {
		fmt.Fprintf(&b, "  context window: %d\n", u.ContextWindow)
	}
	if u.MaxTokens > 0 {
		fmt.Fprintf(&b, "  max output: %d\n", u.MaxTokens)
	}
	if u.Reserve > 0 {
		fmt.Fprintf(&b, "  reserve: %d\n", u.Reserve)
	}
	fmt.Fprintf(&b, "  source: %s", u.Source)
	return b.String(), nil
}
