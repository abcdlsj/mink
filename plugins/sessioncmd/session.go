package sessioncmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/abcdlsj/mink/app"
	"github.com/abcdlsj/mink/command"
)

const sessionUsage = "usage: !session [list|current|new|switch <id>|fork|close]"

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
