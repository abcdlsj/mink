package sessioncmd

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/abcdlsj/sumi/app"
	"github.com/abcdlsj/sumi/bus"
)

const replayUsage = "usage: !replay [count]"

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
	n, ok := replayLimit(args)
	if !ok {
		return replayUsage, nil
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

func replayLimit(args []string) (int, bool) {
	n := 30
	if len(args) == 0 {
		return n, true
	}
	v, err := strconv.Atoi(strings.TrimSpace(args[0]))
	if err != nil || v <= 0 {
		return 0, false
	}
	if v > 200 {
		v = 200
	}
	return v, true
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
