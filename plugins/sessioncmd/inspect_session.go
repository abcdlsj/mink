package sessioncmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/abcdlsj/sumi/app"
	"github.com/abcdlsj/sumi/session"
	"github.com/abcdlsj/sumi/store"
)

type inspectCmd struct {
	app *app.App
}

func (c *inspectCmd) Name() string { return "inspect" }

func (c *inspectCmd) Desc() string {
	return "inspect session snapshot and recent runlog (!inspect [session-id])"
}

func (c *inspectCmd) Run(ctx context.Context, args []string) (string, error) {
	id := strings.TrimSpace(strings.Join(args, " "))
	s, err := c.session(ctx, id)
	if err != nil {
		return "", err
	}
	meta := c.meta(s.ID)
	evs, err := c.app.ReplaySession(s.ID, 12)
	if err != nil {
		return "", err
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Session %s\n", s.ID)
	fmt.Fprintf(&b, "  source: %s\n", s.Source)
	fmt.Fprintf(&b, "  title: %s\n", blank(s.Title, "(untitled)"))
	fmt.Fprintf(&b, "  messages: %d\n", len(s.Messages))
	fmt.Fprintf(&b, "  summary: %s\n", blank(trim(s.Summary, 96), "(none)"))
	if meta.ID != "" {
		fmt.Fprintf(&b, "  snapshot: %s\n", meta.Path)
		fmt.Fprintf(&b, "  runlog: %s\n", meta.RunlogPath)
	}
	if len(evs) > 0 {
		b.WriteString("Recent events:\n")
		for _, ev := range evs {
			fmt.Fprintf(&b, "  %s [%s] %s\n", ev.Time.Format("15:04:05"), ev.Type, replayLine(ev))
		}
	}
	return strings.TrimSpace(b.String()), nil
}

func (c *inspectCmd) session(ctx context.Context, id string) (*session.Session, error) {
	if id == "" {
		return cur(ctx, c.app)
	}
	ss, err := c.app.ListSessions()
	if err != nil {
		return nil, err
	}
	for _, s := range ss {
		if s.ID == id {
			return s, nil
		}
	}
	return nil, fmt.Errorf("session not found: %s", id)
}

func (c *inspectCmd) meta(id string) store.SessionMeta {
	idx, err := c.app.SessionIndex()
	if err != nil {
		return store.SessionMeta{}
	}
	for _, meta := range idx {
		if meta.ID == id {
			return meta
		}
	}
	return store.SessionMeta{}
}
